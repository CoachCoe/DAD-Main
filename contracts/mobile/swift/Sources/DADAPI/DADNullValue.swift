// The literal-null payment-update URL must never decode as a usable redirect.
public struct DADNullValue: Codable, Hashable, Sendable {
    public init() {}

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        guard container.decodeNil() else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Expected null")
        }
    }

    public func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encodeNil()
    }
}
