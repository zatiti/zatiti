import Cocoa
import Darwin
import FlutterMacOS
import Security

// The Runner, rather than Dart, is the only process that resolves and starts
// the installed native credential helper. Its messages carry identifiers and
// redacted dispositions only; the provider key remains in the helper.
final class CredentialCaptureBridge {
  static let channelName = "zatiti/credential_capture"
  static let requestSchema = "zatiti.gui-credential-capture/v1"
  static let resultSchema = "zatiti.gui-credential-capture-result/v1"

  private let channel: FlutterMethodChannel
  private let lock = NSLock()
  private var busy = false

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: Self.channelName, binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, reply in
      guard let self else {
        reply(Self.result("repair_required", "helper_unavailable"))
        return
      }
      guard call.method == "capture", let request = CaptureRequest.parse(call.arguments) else {
        reply(Self.result("repair_required", "invalid_result"))
        return
      }
      lock.lock()
      let wasBusy = busy
      if !wasBusy { busy = true }
      lock.unlock()
      if wasBusy {
        reply(Self.result("retryable", "busy"))
        return
      }
      DispatchQueue.global(qos: .userInitiated).async {
        let response = self.capture(request)
        self.lock.lock()
        self.busy = false
        self.lock.unlock()
        DispatchQueue.main.async { reply(response) }
      }
    }
  }

  private static func result(_ status: String, _ reason: String) -> [String: String] {
    ["schema": resultSchema, "status": status, "reason_code": reason]
  }

  private func capture(_ request: CaptureRequest) -> [String: String] {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    guard installedIdentityMatches(request.installationID, home: home) else {
      return Self.result("repair_required", "installation_mismatch")
    }
    guard let helper = installedHelper(home: home) else {
      return Self.result("repair_required", "helper_unavailable")
    }
    guard trustedHelper(helper) else {
      return Self.result("repair_required", "helper_untrusted")
    }
    let process = Process()
    process.executableURL = helper
    process.arguments = ["capture", "--installation-id", request.installationID,
                         "--connection-id", request.connectionID]
    process.environment = ["HOME": home, "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "LANG": "en_US.UTF-8"]
    process.currentDirectoryURL = URL(fileURLWithPath: home, isDirectory: true)
    process.standardInput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    let output = Pipe()
    process.standardOutput = output
    do {
      try process.run()
    } catch {
      return Self.result("repair_required", "helper_unavailable")
    }
    return readBoundedResult(process, output: output)
  }

  private func readBoundedResult(_ process: Process, output: Pipe) -> [String: String] {
    let fd = output.fileHandleForReading.fileDescriptor
    let oldFlags = fcntl(fd, F_GETFL)
    guard oldFlags >= 0 && fcntl(fd, F_SETFL, oldFlags | O_NONBLOCK) == 0 else {
      process.terminate()
      process.waitUntilExit()
      return Self.result("repair_required", "invalid_result")
    }
    defer { _ = fcntl(fd, F_SETFL, oldFlags) }
    let deadline = Date().addingTimeInterval(180)
    var raw = Data()
    var buffer = [UInt8](repeating: 0, count: 1024)
    let bufferCapacity = buffer.count
    while Date() < deadline {
      let count = buffer.withUnsafeMutableBytes {
        Darwin.read(fd, $0.baseAddress, bufferCapacity)
      }
      if count > 0 {
        raw.append(contentsOf: buffer.prefix(count))
        if raw.count > 4096 {
          process.terminate()
          process.waitUntilExit()
          return Self.result("repair_required", "invalid_result")
        }
      } else if count == 0 {
        process.waitUntilExit()
        guard process.terminationStatus == 0,
              let value = CaptureResponse.parse(raw) else {
          return Self.result("repair_required", "invalid_result")
        }
        return Self.result(value.status, value.reason)
      } else if errno != EAGAIN && errno != EWOULDBLOCK {
        process.terminate()
        process.waitUntilExit()
        return Self.result("repair_required", "invalid_result")
      }
      usleep(20_000)
    }
    process.terminate()
    process.waitUntilExit()
    return Self.result("retryable", "timeout")
  }
}

struct CaptureRequest {
  let installationID: String
  let connectionID: String

  static func parse(_ raw: Any?) -> CaptureRequest? {
    guard let map = raw as? [String: Any], map.count == 3,
          map["schema"] as? String == CredentialCaptureBridge.requestSchema,
          let installation = map["installation_id"] as? String,
          let connection = map["connection_id"] as? String,
          uuid(installation), uuid(connection),
          let data = try? JSONSerialization.data(withJSONObject: map), data.count <= 4096 else {
      return nil
    }
    return CaptureRequest(installationID: installation, connectionID: connection)
  }
}

struct CaptureResponse {
  let status: String
  let reason: String

  static func parse(_ raw: Data) -> CaptureResponse? {
    guard !raw.isEmpty, raw.count <= 4096,
          let json = try? JSONSerialization.jsonObject(with: raw),
          let map = json as? [String: Any], map.count == 3,
          map["schema"] as? String == CredentialCaptureBridge.resultSchema,
          let status = map["status"] as? String,
          let reason = map["reason_code"] as? String else { return nil }
    let allowed: [String: Set<String>] = [
      "completed": ["none"], "cancelled": ["none"],
      "retryable": ["busy", "timeout", "controller_unavailable"],
      "repair_required": ["helper_unavailable", "helper_untrusted", "invalid_result", "installation_mismatch"],
    ]
    guard allowed[status]?.contains(reason) == true else { return nil }
    return CaptureResponse(status: status, reason: reason)
  }
}

private func uuid(_ value: String) -> Bool {
  value.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
              options: .regularExpression) != nil
}

private func owned(_ path: String, type: mode_t, exactMode: mode_t? = nil) -> Bool {
  var info = stat()
  guard lstat(path, &info) == 0,
        info.st_uid == getuid(), info.st_mode & S_IFMT == type,
        (type == S_IFLNK || info.st_mode & 0o022 == 0) else { return false }
  return exactMode == nil || info.st_mode & 0o777 == exactMode
}

private func installedIdentityMatches(_ installationID: String, home: String) -> Bool {
  let state = home + "/Library/Application Support/zatiti"
  let path = state + "/desktop.json"
  guard owned(state, type: S_IFDIR, exactMode: 0o700),
        owned(path, type: S_IFREG, exactMode: 0o600),
        let raw = try? Data(contentsOf: URL(fileURLWithPath: path)), raw.count <= 4096,
        let json = try? JSONSerialization.jsonObject(with: raw),
        let map = json as? [String: Any], map.count == 6,
        map["schema"] as? String == "zatiti.desktop-discovery/v1",
        map["protocol_version"] as? Int == 1,
        map["installation_id"] as? String == installationID,
        map["socket_path"] as? String != nil,
        map["keychain_service"] as? String != nil,
        map["keychain_account"] as? String != nil else { return false }
  return true
}

private func installedHelper(home: String) -> URL? {
  let root = home + "/Library/Application Support/zatiti-dist"
  let current = root + "/current"
  guard owned(root, type: S_IFDIR), owned(root + "/versions", type: S_IFDIR),
        owned(current, type: S_IFLNK) else { return nil }
  var targetBytes = [CChar](repeating: 0, count: 256)
  let n = readlink(current, &targetBytes, targetBytes.count)
  guard n > 0 && n < targetBytes.count else { return nil }
  let target = String(decoding: targetBytes.prefix(n).map { UInt8(bitPattern: $0) }, as: UTF8.self)
  guard target.range(of: "^versions/[A-Za-z0-9][A-Za-z0-9._-]{0,127}$",
                     options: .regularExpression) != nil else { return nil }
  let version = root + "/" + target
  let bin = version + "/bin"
  let helper = bin + "/zatiti-credential-helper"
  guard owned(version, type: S_IFDIR), owned(bin, type: S_IFDIR),
        owned(helper, type: S_IFREG) else { return nil }
  var info = stat()
  guard lstat(helper, &info) == 0, info.st_mode & 0o100 != 0 else { return nil }
  return URL(fileURLWithPath: helper)
}

private func trustedHelper(_ url: URL) -> Bool {
  var selfCode: SecCode?
  guard SecCodeCopySelf([], &selfCode) == errSecSuccess, let selfCode else { return false }
    var signing: CFDictionary?
  var selfStaticCode: SecStaticCode?
  guard SecCodeCopyStaticCode(selfCode, [], &selfStaticCode) == errSecSuccess,
        let selfStaticCode,
        SecCodeCopySigningInformation(selfStaticCode, SecCSFlags(rawValue: kSecCSSigningInformation), &signing) == errSecSuccess,
        let info = signing as? [String: Any],
        let team = info[kSecCodeInfoTeamIdentifier as String] as? String,
        team.range(of: "^[A-Z0-9]{10}$", options: .regularExpression) != nil else { return false }
  let text = "anchor apple generic and identifier \"com.zatiti.credential-helper\" and certificate leaf[subject.OU] = \"\(team)\""
  var requirement: SecRequirement?
  guard SecRequirementCreateWithString(text as CFString, [], &requirement) == errSecSuccess,
        let requirement else { return false }
  var code: SecStaticCode?
  guard SecStaticCodeCreateWithPath(url as CFURL, [], &code) == errSecSuccess,
        let code else { return false }
  return SecStaticCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), requirement) == errSecSuccess
}
