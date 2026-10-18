// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "DADContractCompatibility",
    platforms: [.macOS(.v13), .iOS(.v16)],
    products: [.library(name: "DADAPI", targets: ["DADAPI"])],
    dependencies: [
        .package(url: "https://github.com/apple/swift-openapi-generator", exact: "1.14.1"),
        .package(url: "https://github.com/apple/swift-openapi-runtime", exact: "1.13.1"),
        .package(url: "https://github.com/apple/swift-openapi-urlsession", exact: "1.3.2"),
    ],
    targets: [
        .target(
            name: "DADAPI",
            dependencies: [
                .product(name: "OpenAPIRuntime", package: "swift-openapi-runtime"),
                .product(name: "OpenAPIURLSession", package: "swift-openapi-urlsession"),
            ],
            plugins: [.plugin(name: "OpenAPIGenerator", package: "swift-openapi-generator")]
        ),
        .testTarget(name: "DADAPITests", dependencies: ["DADAPI"]),
    ]
)
