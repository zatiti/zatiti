import AppKit
import Darwin
import Foundation

private let resultSchema = "zatiti.gui-credential-capture-result/v1"
private let prepareSchema = "zatiti.gui-credential-prepare/v1"
private let uuidPattern = try! NSRegularExpression(
  pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
)
private let outputCapacity = 16_384

private struct CaptureArguments {
  let installationID: String
  let connectionID: String

  static func parse(_ args: [String]) -> CaptureArguments? {
    guard args.count == 5, args[0] == "capture", args[1] == "--installation-id",
          args[3] == "--connection-id", validUUID(args[2]), validUUID(args[4]) else {
      return nil
    }
    return CaptureArguments(installationID: args[2], connectionID: args[4])
  }
}

private func validUUID(_ value: String) -> Bool {
  let range = NSRange(value.startIndex..<value.endIndex, in: value)
  return uuidPattern.firstMatch(in: value, range: range)?.range == range
}

private struct BridgeResponse {
  let schema: String
  let status: String
  let reason: String
  let handle: String?
  let provider: String?
  let account: String?

  static func parse(_ bytes: [CChar], count: Int, preparing: Bool) -> BridgeResponse? {
    guard count > 0, count <= outputCapacity else { return nil }
    let data = Data(bytes.prefix(count).map { UInt8(bitPattern: $0) })
    guard let object = try? JSONSerialization.jsonObject(with: data),
          let fields = object as? [String: Any],
          let schema = fields["schema"] as? String,
          let status = fields["status"] as? String,
          let reason = fields["reason_code"] as? String else { return nil }
    if preparing {
      guard schema == prepareSchema else { return nil }
      if status == "ready" {
        guard fields.count == 6, reason == "none",
              let handle = fields["handle"] as? String, handle.count == 64,
              handle.allSatisfy({ $0.isASCII && $0.isHexDigit }),
              let provider = fields["provider"] as? String,
              let account = fields["account_identity"] as? String,
              !provider.isEmpty, !account.isEmpty,
              provider.utf8.count <= 256, account.utf8.count <= 8192 else { return nil }
        return BridgeResponse(schema: schema, status: status, reason: reason,
                              handle: handle, provider: provider, account: account)
      }
      guard fields.count == 3, ["completed", "retryable", "repair_required"].contains(status) else { return nil }
    } else {
      guard schema == resultSchema, fields.count == 3,
            ["completed", "cancelled", "retryable", "repair_required"].contains(status) else { return nil }
    }
    return BridgeResponse(schema: schema, status: status, reason: reason,
                          handle: nil, provider: nil, account: nil)
  }
}

private func redacted(_ status: String, _ reason: String) -> [String: String] {
  ["schema": resultSchema, "status": status, "reason_code": reason]
}

private func normalized(_ value: BridgeResponse) -> [String: String] {
  switch value.status {
  case "completed": return redacted("completed", "none")
  case "cancelled": return redacted("cancelled", "none")
  case "retryable":
    return redacted("retryable", value.reason == "capture_busy" ? "busy" : "controller_unavailable")
  default: return redacted("repair_required", "helper_unavailable")
  }
}

private func callPrepare(_ args: CaptureArguments) -> BridgeResponse? {
  var output = [CChar](repeating: 0, count: outputCapacity)
  let capacity = output.count
  var installation = Array(args.installationID.utf8CString)
  var connection = Array(args.connectionID.utf8CString)
  let count = installation.withUnsafeMutableBufferPointer { first in
    connection.withUnsafeMutableBufferPointer { second in
      ZatitiPrepare(first.baseAddress, 36, second.baseAddress, 36, &output, capacity)
    }
  }
  return BridgeResponse.parse(output, count: Int(count), preparing: true)
}

private func callCancel(_ handle: String) -> BridgeResponse? {
  var output = [CChar](repeating: 0, count: outputCapacity)
  let capacity = output.count
  var handleBytes = Array(handle.utf8CString)
  let count = handleBytes.withUnsafeMutableBufferPointer { pointer in
    ZatitiCancel(pointer.baseAddress, 64, &output, capacity)
  }
  return BridgeResponse.parse(output, count: Int(count), preparing: false)
}

private func callCommit(_ handle: String, credential: inout [UInt8]) -> BridgeResponse? {
  var output = [CChar](repeating: 0, count: outputCapacity)
  let capacity = output.count
  let credentialCount = credential.count
  var handleBytes = Array(handle.utf8CString)
  let count = handleBytes.withUnsafeMutableBufferPointer { pointer in
    credential.withUnsafeMutableBytes { secret in
      ZatitiCommit(pointer.baseAddress, 64, secret.baseAddress?.assumingMemoryBound(to: UInt8.self),
                   credentialCount, &output, capacity)
    }
  }
  return BridgeResponse.parse(output, count: Int(count), preparing: false)
}

private func capture(_ args: CaptureArguments) -> [String: String] {
  guard let prepared = callPrepare(args) else { return redacted("repair_required", "invalid_result") }
  guard prepared.status == "ready", let handle = prepared.handle,
        let provider = prepared.provider, let account = prepared.account else {
    return normalized(prepared)
  }

  let app = NSApplication.shared
  app.setActivationPolicy(.accessory)
  app.activate(ignoringOtherApps: true)
  let alert = NSAlert()
  alert.messageText = "Connect \(provider)"
  alert.informativeText = "Enter the provider key for \(account). It stays in this local helper and Keychain."
  alert.alertStyle = .informational
  alert.addButton(withTitle: "Save provider key")
  alert.addButton(withTitle: "Cancel")
  let field = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 360, height: 28))
  field.placeholderString = "Provider key"
  alert.accessoryView = field
  let choice = alert.runModal()
  guard choice == .alertFirstButtonReturn else {
    _ = callCancel(handle)
    return redacted("cancelled", "none")
  }

  // AppKit owns a transient String; the mutable UTF-8 copy is the narrow C
  // ABI handoff and is zeroed after Commit. No String/byte is logged or sent
  // through stdout, argv, environment, Dart or a public operation.
  let value = field.stringValue
  field.stringValue = ""
  guard !value.isEmpty, value.utf16.count <= 4096 else {
    _ = callCancel(handle)
    return redacted("repair_required", "invalid_result")
  }
  var bytes = Array(value.utf8)
  defer { for index in bytes.indices { bytes[index] = 0 } }
  guard !bytes.isEmpty, bytes.count <= 4096,
        !bytes.contains(10), !bytes.contains(13) else {
    _ = callCancel(handle)
    return redacted("repair_required", "invalid_result")
  }
  guard let committed = callCommit(handle, credential: &bytes) else {
    return redacted("repair_required", "invalid_result")
  }
  return normalized(committed)
}

private func emit(_ result: [String: String]) {
  guard let raw = try? JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]),
        raw.count < 4096 else { return }
  FileHandle.standardOutput.write(raw)
  FileHandle.standardOutput.write(Data([10]))
}

#if HELPER_TESTING
let valid = ["capture", "--installation-id", "11111111-1111-1111-1111-111111111111",
             "--connection-id", "22222222-2222-2222-2222-222222222222"]
precondition(CaptureArguments.parse(valid) != nil)
precondition(CaptureArguments.parse(Array(valid.dropLast())) == nil)
precondition(CaptureArguments.parse(valid + ["synthetic-secret"]) == nil)
precondition(CaptureArguments.parse(["capture", "--installation-id", "ABCDEF12-1111-1111-1111-111111111111",
                                     "--connection-id", valid[4]]) == nil)
let result = redacted("cancelled", "none")
precondition(Set(result.keys) == Set(["schema", "status", "reason_code"]))
let leaked = try! JSONSerialization.data(withJSONObject: [
  "schema": resultSchema, "status": "completed", "reason_code": "none", "secret": "synthetic-secret"
])
precondition(BridgeResponse.parse(leaked.map { CChar(bitPattern: $0) }, count: leaked.count, preparing: false) == nil)
#else
// Keep diagnostics from the Go runtime/AppKit out of the Runner pipe. The
// helper itself writes exactly one small, fixed redacted result to stdout.
let nullFD = Darwin.open("/dev/null", O_WRONLY)
if nullFD >= 0 { _ = dup2(nullFD, STDERR_FILENO); _ = close(nullFD) }
if let args = CaptureArguments.parse(Array(CommandLine.arguments.dropFirst())) {
  emit(capture(args))
} else {
  emit(redacted("repair_required", "invalid_result"))
}
#endif
