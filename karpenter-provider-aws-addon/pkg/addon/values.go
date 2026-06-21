package addon

import (
	"fmt"
	"strings"

	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
)

type DeploymentConfigValues struct {
	AgentInstallNamespace string
	AWSRegion             string
	KarpenterClusterName  string
	ClusterEndpoint       string
	NodeRoleARN           string
	InterruptionQueue     string
}

func ValuesFromDeploymentConfig(
	managedClusterAddOn *addonv1beta1.ManagedClusterAddOn,
	config *addonv1beta1.AddOnDeploymentConfig,
) (DeploymentConfigValues, error) {
	if managedClusterAddOn == nil {
		return DeploymentConfigValues{}, fmt.Errorf("ManagedClusterAddOn is required")
	}
	if config == nil {
		return DeploymentConfigValues{}, fmt.Errorf("ManagedClusterAddOn %s/%s requires AddOnDeploymentConfig", managedClusterAddOn.Namespace, managedClusterAddOn.Name)
	}
	installNamespace := strings.TrimSpace(config.Spec.AgentInstallNamespace)
	if installNamespace == "" {
		return DeploymentConfigValues{}, fmt.Errorf("AddOnDeploymentConfig %s/%s requires spec.agentInstallNamespace", config.Namespace, config.Name)
	}

	variables := map[string]string{}
	for _, variable := range config.Spec.CustomizedVariables {
		variables[variable.Name] = strings.TrimSpace(variable.Value)
	}

	required := []string{
		CustomizedVariableAWSRegion,
		CustomizedVariableKarpenterClusterName,
		CustomizedVariableClusterEndpoint,
		CustomizedVariableNodeRoleARN,
	}
	var missing []string
	for _, name := range required {
		if variables[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return DeploymentConfigValues{}, fmt.Errorf("AddOnDeploymentConfig %s/%s requires customizedVariables %s", config.Namespace, config.Name, strings.Join(missing, ", "))
	}

	return DeploymentConfigValues{
		AgentInstallNamespace: installNamespace,
		AWSRegion:             variables[CustomizedVariableAWSRegion],
		KarpenterClusterName:  variables[CustomizedVariableKarpenterClusterName],
		ClusterEndpoint:       variables[CustomizedVariableClusterEndpoint],
		NodeRoleARN:           variables[CustomizedVariableNodeRoleARN],
		InterruptionQueue:     variables[CustomizedVariableInterruptionQueue],
	}, nil
}
