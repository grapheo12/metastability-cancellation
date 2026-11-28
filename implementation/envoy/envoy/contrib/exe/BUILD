load(
    "//bazel:envoy_build_system.bzl",
    "envoy_cc_binary",
    "envoy_cc_library",
    "envoy_cc_test",
    "envoy_contrib_package",
)
load("//contrib:all_contrib_extensions.bzl", "SELECTED_CONTRIB_EXTENSIONS")

licenses(["notice"])  # Apache 2

envoy_contrib_package()

alias(
    name = "envoy",
    actual = ":envoy-static",
)

envoy_cc_library(
    name = "contrib_version_suffix_lib",
    srcs = ["version_suffix.cc"],
    alwayslink = 1,
)

envoy_cc_binary(
    name = "envoy-static",
    rbe_pool = "linux_x64_small",
    stamped = True,
    visibility = ["//visibility:public"],
    deps = [
        ":contrib_version_suffix_lib",
        "//source/exe:envoy_main_entry_lib",
    ] + SELECTED_CONTRIB_EXTENSIONS,
)

envoy_cc_test(
    name = "contrib_version_test",
    srcs = ["contrib_version_test.cc"],
    deps = [
        ":contrib_version_suffix_lib",
        "//source/common/version:version_lib",
    ],
)

# impl/envoy (cancellation testbed): the standard envoy-static plus only the Peak EWMA load balancing policies (Peak
# EWMA and the testbed's peak_ewma_deadline), the RTT-recording HTTP filter and the testbed's AnxiousRetry upstream
# filter (testbed_anxious), the TokenBucketFilter's upstream filter (testbed_token_bucket) and the BbrFilter's (testbed_bbr), without the other contrib extensions (and their heavy dependencies).
envoy_cc_binary(
    name = "envoy-static-peak-ewma",
    stamped = True,
    visibility = ["//visibility:public"],
    deps = [
        ":contrib_version_suffix_lib",
        "//contrib/peak_ewma/anxious/source:config",
        "//contrib/peak_ewma/filters/http/source:config",
        "//contrib/peak_ewma/load_balancing_policies/source:config",
        "//contrib/peak_ewma/load_balancing_policies/source:deadline_config",
        "//contrib/peak_ewma/bbr/source:config",
        "//contrib/peak_ewma/token_bucket/source:config",
        "//source/exe:envoy_main_entry_lib",
    ],
)
