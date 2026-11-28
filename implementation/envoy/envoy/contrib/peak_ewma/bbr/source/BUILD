load(
    "//bazel:envoy_build_system.bzl",
    "envoy_cc_contrib_extension",
    "envoy_cc_library",
    "envoy_contrib_package",
)

licenses(["notice"])  # Apache 2

envoy_contrib_package()

# impl/envoy (cancellation testbed): reports each upstream attempt's send, answer or loss to its host's BbrState.
envoy_cc_library(
    name = "filter_lib",
    srcs = ["filter.cc"],
    hdrs = ["filter.h"],
    visibility = ["//contrib/peak_ewma/bbr/test:__pkg__"],
    deps = [
        "//contrib/peak_ewma/load_balancing_policies/source:deadline_lb_lib",
        "//envoy/common:random_generator_interface",
        "//envoy/common:time_interface",
        "//envoy/http:filter_interface",
        "//envoy/stream_info:filter_state_interface",
        "//source/extensions/filters/http/common:pass_through_filter_lib",
    ],
)

envoy_cc_contrib_extension(
    name = "config",
    srcs = ["config.cc"],
    hdrs = ["config.h"],
    deps = [
        ":filter_lib",
        "//envoy/api:api_interface",
        "//envoy/registry",
        "//source/extensions/filters/http/common:factory_base_lib",
        "@envoy_api//contrib/envoy/extensions/filters/http/testbed_bbr/v3alpha:pkg_cc_proto",
    ],
)
