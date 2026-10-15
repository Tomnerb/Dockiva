import Foundation
import XCTest
@testable import Dockiva

final class StreamOutputTests: XCTestCase {
    func testDecodesBase64Bytes() throws {
        // "\xe2\x9c" (the first two bytes of "✓") and a lone 0xff, as the core sends them.
        let json = #"{"streamId":"s","containerId":"c","dataBase64":"4pz/","seq":3}"#
        let event = try JSONDecoder().decode(StreamOutputEvent.self, from: Data(json.utf8))
        XCTAssertEqual(event.dataBase64.map { [UInt8]($0) }, [0xE2, 0x9C, 0xFF])
        XCTAssertNil(try JSONDecoder().decode(StreamOutputEvent.self, from: Data(#"{"streamId":"s","containerId":"c","closed":true,"seq":4}"#.utf8)).dataBase64)
    }

    func testUTF8TailCarriesAcrossChunks() {
        var pending: [UInt8] = []
        XCTAssertEqual(decodeUTF8Chunk(&pending, Data([0x6F, 0x6B, 0x20, 0xE2, 0x9C])), "ok ")
        XCTAssertEqual(pending, [0xE2, 0x9C])
        XCTAssertEqual(decodeUTF8Chunk(&pending, Data([0x93, 0x0A])), "✓\n")
        XCTAssertEqual(pending, [])

        // A 4-byte character split 1 + 3, then a long text cut at every position.
        XCTAssertEqual(decodeUTF8Chunk(&pending, Data([0xF0])), "")
        XCTAssertEqual(decodeUTF8Chunk(&pending, Data([0x9F, 0x90, 0xB3])), "🐳")
        let text = String(repeating: "héllo ✓ 🐳 ", count: 50)
        let bytes = Array(text.utf8)
        for cut in 0...bytes.count {
            var tail: [UInt8] = []
            let joined = decodeUTF8Chunk(&tail, Data(bytes[..<cut])) + decodeUTF8Chunk(&tail, Data(bytes[cut...]))
            XCTAssertEqual(joined, text, "cut at \(cut)")
        }

        // Bytes that are not UTF-8 become U+FFFD and are not held back.
        XCTAssertEqual(decodeUTF8Chunk(&pending, Data([0x61, 0xFF])), "a\u{FFFD}")
        XCTAssertEqual(pending, [])
    }
}
