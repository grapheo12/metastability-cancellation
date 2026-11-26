load(
    "//bazel:envoy_build_system.bzl",
    "envoy_cc_contrib_extension",
    "envoy_cc_library",
    "envoy_contrib_package",
)

licenses(["notice"])  # Apache 2

envoy_contrib_package()

# impl/envoy (cancellation testbed): the AnxiousTracker of the anxious RetryPolicy (pure logic).
envoy_cc_library(
    name = "anxious_tracker_lib",
    srcs = ["anxious_tracker.cc"],
    hdrs = ["anxious_tracker.h"],
    visibility = ["//contrib/peak_ewma/anxious/test:__pkg__"],
    deps = [
        "@abseil-cpp//absl/strings",
        "@abseil-cpp//absl/synchronization",
        "@abseil-cpp//absl/types:optional",
    ],
)

envoy_cc_library(
    name = "filter_lib",
    srcs = ["filter.cc"],
    hdrs = ["filter.h"],
    visibility = ["//contrib/peak_ewma/anxious/test:__pkg__"],
    deps = [
        ":anxious_tracker_lib",
        "//envoy/common:time_interface",
        "//envoy/http:filter_interface",
        "//envoy/stream_info:filter_state_interface",
        "//source/common/http:utility_lib",
        "//source/common/router:testbed_retry_hook_lib",
        "//source/extensions/filters/http/common:pass_through_filter_lib",
        "@abseil-cpp//absl/strings",
        "@abseil-cpp//absl/strings:str_format",
        "@abseil-cpp//absl/synchronization",
        "@envoy_api//contrib/envoy/extensions/filters/http/testbed_anxious/v3alpha:pkg_cc_proto",
    ],
)

envoy_cc_contrib_extension(
    name = "config",
    srcs = ["config.cc"],
    hdrs = ["config.h"],
    deps = [
        ":filter_lib",
        "//envoy/registry",
        "//source/extensions/filters/http/common:factory_base_lib",
        "@abseil-cpp//absl/synchronization",
        "@envoy_api//contrib/envoy/extensions/filters/http/testbed_anxious/v3alpha:pkg_cc_proto",
    ],
)
