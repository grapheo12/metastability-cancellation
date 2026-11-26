load(
    "//bazel:envoy_build_system.bzl",
    "envoy_cc_contrib_extension",
    "envoy_cc_library",
    "envoy_contrib_package",
)

licenses(["notice"])  # Apache 2

envoy_contrib_package()

envoy_cc_library(
    name = "peak_ewma_lb_lib",
    srcs = [
        "cost.cc",
        "host_data.cc",
        "peak_ewma_lb.cc",
    ],
    hdrs = [
        "cost.h",
        "host_data.h",
        "peak_ewma_lb.h",
    ],
    visibility = [
        "//contrib/peak_ewma/filters/http/source:__pkg__",
        "//contrib/peak_ewma/filters/http/test:__pkg__",
        "//contrib/peak_ewma/load_balancing_policies/test:__pkg__",
        "//test/extensions/load_balancing_policies/peak_ewma:__pkg__",
    ],
    deps = [
        "//source/common/common:utility_lib",
        "//source/common/config:utility_lib",
        "//source/common/runtime:runtime_lib",
        "//source/common/upstream:upstream_lib",
        "//source/extensions/load_balancing_policies/common:factory_base",
        "//source/extensions/load_balancing_policies/common:load_balancer_lib",
        "@envoy_api//contrib/envoy/extensions/load_balancing_policies/peak_ewma/v3alpha:pkg_cc_proto",
    ],
)

envoy_cc_contrib_extension(
    name = "config",
    srcs = ["config.cc"],
    hdrs = ["config.h"],
    deps = [
        ":peak_ewma_lb_lib",
        "//envoy/registry",
        "//source/extensions/load_balancing_policies/common:factory_base",
        "@envoy_api//contrib/envoy/extensions/load_balancing_policies/peak_ewma/v3alpha:pkg_cc_proto",
    ],
)

# impl/envoy (cancellation testbed): Peak EWMA with a deadline filter over every host and a FIFO wait queue.
envoy_cc_library(
    name = "deadline_lb_lib",
    srcs = ["deadline_lb.cc"],
    hdrs = ["deadline_lb.h"],
    visibility = ["//contrib/peak_ewma/load_balancing_policies/test:__pkg__"],
    deps = [
        ":peak_ewma_lb_lib",
        "//envoy/event:dispatcher_interface",
        "//envoy/event:timer_interface",
        "//envoy/http:header_map_interface",
        "//envoy/network:connection_interface",
        "//envoy/stream_info:stream_info_interface",
        "//envoy/stream_info:filter_state_interface",
        "//envoy/upstream:upstream_interface",
        "//source/common/protobuf:utility_lib",
        "@abseil-cpp//absl/status",
        "@abseil-cpp//absl/strings",
        "@abseil-cpp//absl/synchronization",
        "@abseil-cpp//absl/types:optional",
        "@envoy_api//contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha:pkg_cc_proto",
    ],
)

envoy_cc_contrib_extension(
    name = "deadline_config",
    srcs = ["deadline_config.cc"],
    hdrs = ["deadline_config.h"],
    deps = [
        ":deadline_lb_lib",
        "//envoy/registry",
        "//source/extensions/load_balancing_policies/common:factory_base",
        "@envoy_api//contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha:pkg_cc_proto",
    ],
)
