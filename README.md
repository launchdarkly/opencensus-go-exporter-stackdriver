# OpenCensus Go Stackdriver - LaunchDarkly fork

This is a fork of the last release of `contrib.go.opencensus.io/exporter/stackdriver`, the OpenCensus client for Google Cloud Trace, formerly known as StackDriver. It is used by the [LaunchDarkly Relay Proxy](https://github.com/launchdarkly/ld-relay). The fork exists to allow security patches, until LaunchDarkly is able to abandon the OpenCensus API and switch to OpenTelemetry. These patches may involve disabling functionality that LaunchDarkly does not use. LaunchDarkly provides no support for use of this code for any other purpose.

For more information about this library, see the original repository.
