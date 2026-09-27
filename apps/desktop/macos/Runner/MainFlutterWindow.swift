import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  private var credentialCapture: CredentialCaptureBridge?
  private var voiceAudio: VoiceAudioBridge?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)
    voiceAudio = VoiceAudioBridge(messenger: flutterViewController.engine.binaryMessenger)
    credentialCapture = CredentialCaptureBridge(messenger: flutterViewController.engine.binaryMessenger)

    super.awakeFromNib()
  }
}

// Audio stays in the native runner. This bridge has no provider credentials,
// network client or worker authority; Dart talks only to controller operations.
import AVFoundation

final class VoiceAudioBridge: NSObject, FlutterStreamHandler {
  private let methods: FlutterMethodChannel
  private let events: FlutterEventChannel
  private var sink: FlutterEventSink?
  private let engine = AVAudioEngine()
  private let player = AVAudioPlayerNode()
  private let queue = DispatchQueue(label: "zatiti.voice.capture")
  private var active = false
  private var captured = Data()
  private var silence = 0
  private var spoken = 0
  private var playing = false
  private var generation = 0
  private var playbackGeneration = 0
  private var playCompletion: FlutterResult?
  private var converter: AVAudioConverter?
  private var observer: NSObjectProtocol?
  private let pcm = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: true)!

  init(messenger: FlutterBinaryMessenger) {
    methods = FlutterMethodChannel(name: "zatiti/voice", binaryMessenger: messenger)
    events = FlutterEventChannel(name: "zatiti/voice/events", binaryMessenger: messenger)
    super.init()
    engine.attach(player)
    events.setStreamHandler(self)
    methods.setMethodCallHandler { [weak self] call, reply in
      guard let self = self else { return }
      switch call.method {
      case "start":
        self.stopCapture()
        self.generation += 1
        let startGeneration = self.generation
        AVCaptureDevice.requestAccess(for: .audio) { granted in
          DispatchQueue.main.async {
            guard self.generation == startGeneration else {
              reply(FlutterError(code: "start_cancelled", message: "Voice session ended before microphone access completed.", details: nil)); return
            }
            guard granted else { reply(FlutterError(code: "microphone_denied", message: "Allow microphone access in System Settings.", details: nil)); return }
            do { try self.start(); reply(nil) }
            catch { self.stop(); reply(FlutterError(code: "audio_unavailable", message: "The audio device is unavailable.", details: nil)) }
          }
        }
      case "stop": self.stop(); reply(nil)
      case "interrupt": self.interruptPlayback(); reply(nil)
      case "play":
        guard let args = call.arguments as? [String: Any], let bytes = args["audio"] as? FlutterStandardTypedData, bytes.data.count <= 8 * 1024 * 1024 else {
          reply(FlutterError(code: "invalid_audio", message: "Invalid speech audio.", details: nil)); return
        }
        do { try self.play(bytes.data) { _ in reply(nil) } }
        catch { reply(FlutterError(code: "playback_failed", message: "Speech could not be played.", details: nil)) }
      default: reply(FlutterMethodNotImplemented)
      }
    }
    observer = NotificationCenter.default.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main) { [weak self] _ in
      self?.stop(); self?.emit(["type": "stopped"])
    }
  }
  deinit { if let observer = observer { NotificationCenter.default.removeObserver(observer) } }
  func onListen(withArguments arguments: Any?, eventSink events: @escaping FlutterEventSink) -> FlutterError? { sink = events; return nil }
  func onCancel(withArguments arguments: Any?) -> FlutterError? { stop(); sink = nil; return nil }
  private func emit(_ event: [String: Any]) { DispatchQueue.main.async { [weak self] in self?.sink?(event) } }
  private func start() throws {
    let input = engine.inputNode
    // Use the OS echo-cancelled duplex path; speaker behavior still requires
    // native qualification on each supported device.
    if #available(macOS 10.15, *) {
      try input.setVoiceProcessingEnabled(true)
    } else {
      throw NSError(domain: "voice", code: 4)
    }
    let format = input.outputFormat(forBus: 0)
    guard format.sampleRate > 0, format.channelCount > 0 else { throw NSError(domain: "voice", code: 1) }
    converter = AVAudioConverter(from: format, to: pcm)
    engine.connect(player, to: engine.mainMixerNode, format: nil)
    active = true
    input.installTap(onBus: 0, bufferSize: 2048, format: format) { [weak self] buffer, _ in
      guard let self = self, let converter = self.converter else { return }
      let capacity = AVAudioFrameCount(Double(buffer.frameLength) * 16000 / format.sampleRate + 1)
      guard let output = AVAudioPCMBuffer(pcmFormat: self.pcm, frameCapacity: capacity) else { return }
      var supplied = false
      var error: NSError?
      converter.convert(to: output, error: &error) { _, status in
        if supplied { status.pointee = .noDataNow; return nil }
        supplied = true; status.pointee = .haveData; return buffer
      }
      guard error == nil, let samples = output.int16ChannelData?[0] else { return }
      let count = Int(output.frameLength)
      let data = Data(bytes: samples, count: count * 2)
      var energy: Double = 0
      for i in 0..<count { let sample = Double(samples[i]) / 32768; energy += sample * sample }
      let level = count == 0 ? 0 : sqrt(energy / Double(count))
      self.queue.async { [weak self] in self?.consume(data, level: level, samples: count) }
    }
    engine.prepare(); try engine.start()
  }
  private func consume(_ data: Data, level: Double, samples: Int) {
    guard active else { return }
    if level > 0.018 {
      if captured.isEmpty { emit(["type": "speech_start"]); DispatchQueue.main.async { [weak self] in self?.interruptPlayback() } }
      silence = 0; spoken += samples
    } else if captured.isEmpty { return } else { silence += samples }
    captured.append(data)
    if silence >= 12000 || captured.count >= 480000 {
      let audio = Data(captured.prefix(480000)); let useful = spoken >= 3200
      captured.removeAll(keepingCapacity: true); silence = 0; spoken = 0
      if useful { emit(["type": "utterance", "audio": FlutterStandardTypedData(bytes: wav(audio))]) }
    }
  }
  private func wav(_ data: Data) -> Data {
    var out = Data()
    func text(_ value: String) { out.append(value.data(using: .ascii)!) }
    func u16(_ value: UInt16) { var v = value.littleEndian; withUnsafeBytes(of: &v) { out.append(contentsOf: $0) } }
    func u32(_ value: UInt32) { var v = value.littleEndian; withUnsafeBytes(of: &v) { out.append(contentsOf: $0) } }
    text("RIFF"); u32(UInt32(data.count + 36)); text("WAVEfmt "); u32(16); u16(1); u16(1); u32(16000); u32(32000); u16(2); u16(16); text("data"); u32(UInt32(data.count)); out.append(data); return out
  }
  private func play(_ data: Data, completion: @escaping FlutterResult) throws {
    guard active else { throw NSError(domain: "voice", code: 2) }
    let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".mp3")
    try data.write(to: url, options: .atomic)
    defer { try? FileManager.default.removeItem(at: url) }
    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    let file = try AVAudioFile(forReading: url)
    guard file.length <= 24000 * 300, let buffer = AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: AVAudioFrameCount(file.length)) else { throw NSError(domain: "voice", code: 3) }
    try file.read(into: buffer)
    player.stop()
    engine.disconnectNodeOutput(player)
    engine.connect(player, to: engine.mainMixerNode, format: file.processingFormat)
    playbackGeneration += 1
    let token = playbackGeneration
    playCompletion = completion
    playing = true
    player.scheduleBuffer(buffer, completionCallbackType: .dataPlayedBack) { [weak self] _ in
      DispatchQueue.main.async {
        guard let self = self, self.playbackGeneration == token else { return }
        self.playing = false
        self.emit(["type": "playback_ended"])
        self.finishPlaybackCall()
      }
    }
    player.play()
  }
  private func interruptPlayback() {
    playbackGeneration += 1
    player.stop(); playing = false
    finishPlaybackCall()
  }
  private func finishPlaybackCall() {
    let completion = playCompletion
    playCompletion = nil
    completion?(nil)
  }
  private func stop() {
    generation += 1
    stopCapture()
  }
  private func stopCapture() {
    engine.stop(); interruptPlayback()
    if active { engine.inputNode.removeTap(onBus: 0) }
    queue.sync { active = false; captured.removeAll(); silence = 0; spoken = 0 }
    converter = nil
  }
}
