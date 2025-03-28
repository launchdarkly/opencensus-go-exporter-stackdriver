// Copyright 2020, OpenCensus Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package monitoredresource

import (
	"os"
	"testing"
)

const (
	GCPProjectIDStr     = "gcp-project"
	GCPInstanceIDStr    = "instance"
	GCPZoneStr          = "us-east1"
	GKENamespaceStr     = "namespace"
	GKEPodIDStr         = "pod-id"
	GKEContainerNameStr = "container"
	GKEClusterNameStr   = "cluster"
)

func TestGKEContainerMonitoredResourcesV2(t *testing.T) {
	os.Setenv("KUBERNETES_SERVICE_HOST", "127.0.0.1")
	autoDetected := GKEContainer{
		InstanceID:    GCPInstanceIDStr,
		ProjectID:     GCPProjectIDStr,
		Zone:          GCPZoneStr,
		ClusterName:   GKEClusterNameStr,
		ContainerName: GKEContainerNameStr,
		NamespaceID:   GKENamespaceStr,
		PodID:         GKEPodIDStr,
	}

	resType, labels := autoDetected.MonitoredResource()
	if resType != "k8s_container" ||
		labels["project_id"] != GCPProjectIDStr ||
		labels["cluster_name"] != GKEClusterNameStr ||
		labels["container_name"] != GKEContainerNameStr ||
		labels["location"] != GCPZoneStr ||
		labels["namespace_name"] != GKENamespaceStr ||
		labels["pod_name"] != GKEPodIDStr {
		t.Errorf("GKEContainerMonitoredResourceV2 Failed: %v", autoDetected)
	}
}

func TestGCEInstanceMonitoredResources(t *testing.T) {
	os.Setenv("KUBERNETES_SERVICE_HOST", "")
	autoDetected := GCEInstance{
		InstanceID: GCPInstanceIDStr,
		ProjectID:  GCPProjectIDStr,
		Zone:       GCPZoneStr,
	}

	resType, labels := autoDetected.MonitoredResource()
	if resType != "gce_instance" ||
		labels["instance_id"] != GCPInstanceIDStr ||
		labels["project_id"] != GCPProjectIDStr ||
		labels["zone"] != GCPZoneStr {
		t.Errorf("GCEInstanceMonitoredResource Failed: %v", autoDetected)
	}
}

// REMOVED IN LAUNCHDARKLY FORK - BEGIN
// func TestAWSEC2InstanceMonitoredResources(t *testing.T) {
// 	autoDetected := AWSEC2Instance{
// 		AWSAccount: "123456789012",
// 		InstanceID: "i-1234567890abcdef0",
// 		Region:     "aws:us-west-2",
// 	}

// 	resType, labels := autoDetected.MonitoredResource()
// 	if resType != "aws_ec2_instance" ||
// 		labels["instance_id"] != "i-1234567890abcdef0" ||
// 		labels["aws_account"] != "123456789012" ||
// 		labels["region"] != "aws:us-west-2" {
// 		t.Errorf("AWSEC2InstanceMonitoredResource Failed: %v", autoDetected)
// 	}
// }
// REMOVED IN LAUNCHDARKLY FORK - END
