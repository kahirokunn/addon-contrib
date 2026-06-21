package addon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStaticManifestsDeclareOCMTemplateContract(t *testing.T) {
	for _, path := range []string{
		"../../deploy/resources/cluster-management-addon.yaml",
		"../../charts/karpenter-provider-aws-addon/templates/cluster-management-addon.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			docs := readYAMLDocuments(t, path)
			cma := findDocument(t, docs, "ClusterManagementAddOn", AddonName)
			annotations := stringMap(nestedMap(cma, "metadata")["annotations"])
			if annotations["addon.open-cluster-management.io/lifecycle"] != "addon-manager" {
				t.Fatalf("ClusterManagementAddOn lifecycle annotation = %q, want addon-manager", annotations["addon.open-cluster-management.io/lifecycle"])
			}
			if got := nestedMap(nestedMap(cma, "spec"), "installStrategy")["type"]; got != "Manual" {
				t.Fatalf("ClusterManagementAddOn installStrategy.type = %v, want Manual", got)
			}
			assertSupportedConfig(t, cma, "addon.open-cluster-management.io", "addontemplates")
			assertSupportedConfig(t, cma, "addon.open-cluster-management.io", "addondeploymentconfigs")

			for _, templateName := range []string{DefaultAddOnTemplateName, HostedAddOnTemplateName} {
				template := findDocument(t, docs, "AddOnTemplate", templateName)
				if got := nestedMap(template, "spec")["addonName"]; got != AddonName {
					t.Fatalf("AddOnTemplate %s spec.addonName = %v, want %s", templateName, got, AddonName)
				}
			}
		})
	}
}

func TestPerClusterExamplesUseAddOnDeploymentConfigContract(t *testing.T) {
	for _, path := range []string{
		"../../deploy/examples/default-managedclusteraddon.yaml",
		"../../deploy/examples/hosted-managedclusteraddon.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			if bytes.Contains(raw, []byte(AnnotationPrefix)) {
				t.Fatalf("example must not use legacy Karpenter annotations")
			}

			docs := decodeYAMLDocuments(t, raw)
			config := findDocument(t, docs, "AddOnDeploymentConfig", AddonName)
			spec := nestedMap(config, "spec")
			if spec["agentInstallNamespace"] == "" {
				t.Fatalf("AddOnDeploymentConfig spec.agentInstallNamespace is required")
			}
			for _, name := range []string{
				CustomizedVariableAWSRegion,
				CustomizedVariableKarpenterClusterName,
				CustomizedVariableClusterEndpoint,
				CustomizedVariableNodeRoleARN,
			} {
				assertCustomizedVariable(t, config, name)
			}

			mca := findDocument(t, docs, "ManagedClusterAddOn", AddonName)
			assertManagedClusterAddOnConfig(t, mca, "addon.open-cluster-management.io", "addontemplates")
			assertManagedClusterAddOnConfig(t, mca, "addon.open-cluster-management.io", "addondeploymentconfigs")
		})
	}
}

func TestHubRBACIsScopedToPrerequisitesAndConfigReads(t *testing.T) {
	for _, path := range []string{
		"../../deploy/resources/cluster-role.yaml",
		"../../charts/karpenter-provider-aws-addon/templates/clusterrole.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatalf("read rbac: %v", err)
			}
			for _, forbidden := range []string{
				`resources: ["events"]`,
				`resources: ["leases"]`,
				`resources: ["managedclusteraddons/status"]`,
				`verbs: ["get", "list", "watch", "create", "patch", "update"]`,
			} {
				if bytes.Contains(raw, []byte(forbidden)) {
					t.Fatalf("RBAC contains forbidden grant %s", forbidden)
				}
			}
			if !bytes.Contains(raw, []byte(`resources: ["addondeploymentconfigs"]`)) {
				t.Fatalf("RBAC must allow reading addondeploymentconfigs")
			}
		})
	}
}

func readYAMLDocuments(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return decodeYAMLDocuments(t, raw)
}

func decodeYAMLDocuments(t *testing.T, raw []byte) []map[string]interface{} {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []map[string]interface{}
	for {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode yaml: %v", err)
		}
		if len(doc) == 0 {
			continue
		}
		docs = append(docs, doc)
	}
	return docs
}

func findDocument(t *testing.T, docs []map[string]interface{}, kind, name string) map[string]interface{} {
	t.Helper()
	for _, doc := range docs {
		if doc["kind"] != kind {
			continue
		}
		metadata := nestedMap(doc, "metadata")
		if metadata["name"] == name {
			return doc
		}
	}
	t.Fatalf("document %s/%s not found", kind, name)
	return nil
}

func assertSupportedConfig(t *testing.T, cma map[string]interface{}, group, resource string) {
	t.Helper()
	for _, item := range nestedSlice(nestedMap(cma, "spec"), "supportedConfigs") {
		config := item.(map[string]interface{})
		if config["group"] == group && config["resource"] == resource {
			return
		}
	}
	t.Fatalf("supportedConfig %s/%s not found", group, resource)
}

func assertManagedClusterAddOnConfig(t *testing.T, mca map[string]interface{}, group, resource string) {
	t.Helper()
	for _, item := range nestedSlice(nestedMap(mca, "spec"), "configs") {
		config := item.(map[string]interface{})
		if config["group"] == group && config["resource"] == resource {
			return
		}
	}
	t.Fatalf("ManagedClusterAddOn spec.configs missing %s/%s", group, resource)
}

func assertCustomizedVariable(t *testing.T, config map[string]interface{}, name string) {
	t.Helper()
	for _, item := range nestedSlice(nestedMap(config, "spec"), "customizedVariables") {
		variable := item.(map[string]interface{})
		if variable["name"] == name && variable["value"] != "" {
			return
		}
	}
	t.Fatalf("customized variable %s not found", name)
}

func nestedMap(in map[string]interface{}, key string) map[string]interface{} {
	out, _ := in[key].(map[string]interface{})
	return out
}

func nestedSlice(in map[string]interface{}, key string) []interface{} {
	out, _ := in[key].([]interface{})
	return out
}

func stringMap(in interface{}) map[string]string {
	out := map[string]string{}
	values, _ := in.(map[string]interface{})
	for key, value := range values {
		text, _ := value.(string)
		out[key] = text
	}
	return out
}
