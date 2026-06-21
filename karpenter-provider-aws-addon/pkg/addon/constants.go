package addon

const (
	AddonName = "karpenter-provider-aws"

	DefaultAddOnTemplateName = AddonName + "-default"
	HostedAddOnTemplateName  = AddonName + "-hosted"

	AnnotationPrefix = "karpenter-provider-aws-addon.open-cluster-management.io/"

	CustomizedVariableAWSRegion            = "AWS_REGION"
	CustomizedVariableKarpenterClusterName = "KARPENTER_CLUSTER_NAME"
	CustomizedVariableClusterEndpoint      = "CLUSTER_ENDPOINT"
	CustomizedVariableNodeRoleARN          = "NODE_ROLE_ARN"
	CustomizedVariableInterruptionQueue    = "INTERRUPTION_QUEUE"
	AddOnConfigGroup                       = "addon.open-cluster-management.io"
	AddOnDeploymentConfigResource          = "addondeploymentconfigs"

	DeploymentName           = "karpenter-provider-aws"
	KarpenterContainerName   = "karpenter"
	IRSASidecarContainerName = "aws-irsa-sidecar"
	ServiceAccountName       = "karpenter-provider-aws"

	ManagedServiceAccountAddonName               = "managed-serviceaccount"
	DefaultManagedServiceAccountInstallNamespace = "open-cluster-management-agent-addon"

	KarpenterDNSRoleNamespace = "kube-system"

	ManagedKubeconfigSecretName = AddonName + "-managed-kubeconfig"
	ManagedKubeconfigSecretKey  = "kubeconfig"
	ManagedKubeconfigVolumeName = "managed-kubeconfig-secret"
	ManagedKubeconfigPath       = "/managed/config/kubeconfig"
	ManagedTokenPath            = "/managed/config/token"
	ManagedCAPath               = "/managed/config/ca.crt"
	AWSConfigPath               = "/var/run/aws-irsa/config"
	AWSTokenPath                = "/var/run/aws-irsa/token"
	AWSIRSAStateVolumeName      = "aws-irsa-state"

	OwnerLabelKey         = "app.kubernetes.io/managed-by"
	OwnerLabelValue       = "karpenter-provider-aws-addon"
	TargetClusterLabelKey = AnnotationPrefix + "target-cluster"
	Finalizer             = AnnotationPrefix + "hosting-secret-cleanup"
	FieldManager          = "karpenter-provider-aws-addon"
)
