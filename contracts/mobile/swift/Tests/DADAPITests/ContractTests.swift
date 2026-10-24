import Foundation
import XCTest
import DADAPI

final class ContractTests: XCTestCase {
    func testNativeSessionRequiresBothCredentials() throws {
        let body = Data(#"{"access_token":"synthetic-access","expires_in":900,"refresh_token":"synthetic-refresh","refresh_expires_in":5184000}"#.utf8)
        let session = try JSONDecoder().decode(Components.Schemas.NativeSession.self, from: body)
        XCTAssertEqual(session.access_token, "synthetic-access")
        XCTAssertEqual(session.refresh_token, "synthetic-refresh")
        let missingRefresh = Data(#"{"access_token":"synthetic-access","expires_in":900,"refresh_expires_in":5184000}"#.utf8)
        XCTAssertThrowsError(try JSONDecoder().decode(Components.Schemas.NativeSession.self, from: missingRefresh))
    }

    func testProfileDecodesNullablePublicName() throws {
        let body = Data(#"{"email":"donor@example.org","public_name":null,"account_role":"DONOR","zip":null,"city":null,"timezone":null,"phone":null,"referral_emails":true,"marketing_emails":false,"push_referral_converted":true,"push_streak_at_risk":true,"push_streak_milestone":true,"version":3,"consent_version":"synthetic-v1"}"#.utf8)
        let profile = try JSONDecoder().decode(Components.Schemas.Profile.self, from: body)
        // Decoding with account_role (and the other new required identity
        // fields) present is the contract check: the generated Profile now
        // requires them, so a fixture missing them would fail to decode.
        XCTAssertNil(profile.public_name)
        XCTAssertEqual(profile.version, 3)
        XCTAssertFalse(profile.marketing_emails)
    }

    func testProfileDecodesPopulatedIdentityFields() throws {
        let body = Data(#"{"email":"donor@example.org","public_name":"Dana","account_role":"DONOR","zip":"94103","city":"San Francisco","timezone":"America/Los_Angeles","phone":"+14155550123","referral_emails":true,"marketing_emails":false,"push_referral_converted":true,"push_streak_at_risk":true,"push_streak_milestone":true,"version":1,"consent_version":"synthetic-v1"}"#.utf8)
        let profile = try JSONDecoder().decode(Components.Schemas.Profile.self, from: body)
        XCTAssertEqual(profile.zip, "94103")
        XCTAssertEqual(profile.timezone, "America/Los_Angeles")
        XCTAssertEqual(profile.phone, "+14155550123")
    }
}
