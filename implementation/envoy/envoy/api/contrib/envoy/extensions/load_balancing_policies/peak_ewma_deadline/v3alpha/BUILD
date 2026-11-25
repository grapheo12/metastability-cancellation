# impl/envoy (cancellation testbed): written by hand in the style of tools/proto_format/proto_sync.py output.

load("@envoy_api//bazel:api_build_system.bzl", "api_proto_package")

licenses(["notice"])  # Apache 2

api_proto_package(
    deps = [
        "//contrib/envoy/extensions/load_balancing_policies/peak_ewma/v3alpha:pkg",
        "@xds//udpa/annotations:pkg",
        "@xds//xds/annotations/v3:pkg",
    ],
)
