import Cocoa
import FlutterMacOS
import XCTest
@testable import zatiti_desktop

class RunnerTests: XCTestCase {

  func testCredentialCaptureAcceptsOnlyTwoIdentifiers() {
    let request = CaptureRequest.parse([
      "schema": CredentialCaptureBridge.requestSchema,
      "installation_id": "11111111-1111-1111-1111-111111111111",
      "connection_id": "22222222-2222-2222-2222-222222222222",
    ])
    XCTAssertNotNil(request)
    XCTAssertNil(CaptureRequest.parse([
      "schema": CredentialCaptureBridge.requestSchema,
      "installation_id": "11111111-1111-1111-1111-111111111111",
      "connection_id": "22222222-2222-2222-2222-222222222222",
      "provider_key": "synthetic-secret",
    ]))
    XCTAssertNil(CaptureRequest.parse([
      "schema": CredentialCaptureBridge.requestSchema,
      "installation_id": "UPPERCASE",
      "connection_id": "22222222-2222-2222-2222-222222222222",
    ]))
  }

  func testCredentialCaptureRejectsSecretBearingResult() throws {
    let valid = try JSONSerialization.data(withJSONObject: [
      "schema": CredentialCaptureBridge.resultSchema,
      "status": "completed", "reason_code": "none",
    ])
    XCTAssertEqual(CaptureResponse.parse(valid)?.status, "completed")
    let leaked = try JSONSerialization.data(withJSONObject: [
      "schema": CredentialCaptureBridge.resultSchema,
      "status": "completed", "reason_code": "none",
      "credential": "synthetic-secret",
    ])
    XCTAssertNil(CaptureResponse.parse(leaked))
  }

}
