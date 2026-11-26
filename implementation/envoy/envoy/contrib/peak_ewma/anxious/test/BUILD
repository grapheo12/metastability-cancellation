load(
    "//bazel:envoy_build_system.bzl",
    "envoy_cc_test",
    "envoy_contrib_package",
)

licenses(["notice"])  # Apache 2

envoy_contrib_package()

# impl/envoy (cancellation testbed): the AnxiousTracker against the spec's worked examples.
envoy_cc_test(
    name = "anxious_tracker_test",
    srcs = ["anxious_tracker_test.cc"],
    deps = [
        "//contrib/peak_ewma/anxious/source:anxious_tracker_lib",
        "//contrib/peak_ewma/anxious/source:filter_lib",
    ],
)
