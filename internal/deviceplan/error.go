package deviceplan

import "fmt"

// Field and behavior identifiers attached to planning errors. They are part of the
// machine-readable plan contract consumed by renderers and tooling.
const (
	fieldDefinitionStartupConfig       = "definition.startup-config"
	fieldKind                          = "kind"
	fieldDeploymentComponents          = "deployment.components"
	fieldDeploymentOperationsTarget    = "deployment.operations.target"
	fieldWorkspace                     = "workspace"
	fieldPayloads                      = "payloads"
	fieldPayloadsURL                   = "payloads.url"
	fieldCertificates                  = "certificates"
	fieldPreparationRuntimeArtifacts   = "preparation.runtimeArtifacts"
	fieldPreparationPayloads           = "preparation.payloads"
	fieldPreparationDestination        = "preparation.destination"
	fieldHostsSnapshot                 = "hosts.snapshot"
	fieldCopySource                    = "copy.source"
	fieldCopySnapshot                  = "copy.snapshot"
	behaviorImportedStartupConfig      = "imported-startup-config"
	behaviorImportedInit               = "imported-init"
	behaviorImportedDeploy             = "imported-deploy"
	behaviorImportedComponents         = "imported-components"
	behaviorImportedCertificateRequest = "imported-certificate-request"
	behaviorPayloadWorkspace           = "payload-workspace"
	behaviorPayloadStaging             = "payload-staging"
	behaviorArtifactGeneration         = "artifact-generation"
	behaviorCertificateMaterial        = "certificate-material"
	behaviorNetworkNamespace           = "network-namespace"
	behaviorRuntimeCreateContainer     = "runtime.CreateContainer"
	behaviorURLPayload                 = "url-payload"
	filesystemTmpfs                    = "tmpfs"
	// plannerIdentity is the planner name recorded in discovered image and mapper identities and
	// used as the certificate organization, identifying clabernetes-issued material.
	plannerIdentity = "clabernetes"
)

// Error is a stable, structured planning diagnostic. It contains identities and field paths but
// never rejected values, payload content, credentials, or other secret material.
type Error struct {
	Code     ErrorCode `json:"code"`
	NodeID   string    `json:"nodeID,omitempty"`
	Field    string    `json:"field,omitempty"`
	Behavior string    `json:"behavior,omitempty"`
	Message  string    `json:"message"`
	cause    error
}

// Error returns a bounded diagnostic suitable for a Node condition or Kubernetes Event.
func (e *Error) Error() string {
	message := fmt.Sprintf("device planning %s", e.Code)
	if e.NodeID != "" {
		message += " for Node " + e.NodeID
	}

	if e.Field != "" {
		message += " at " + e.Field
	}

	if e.Behavior != "" {
		message += " (" + e.Behavior + ")"
	}

	if e.Message != "" {
		message += ": " + e.Message
	}

	return message
}

// Unwrap exposes only causes that callers deliberately supplied without sensitive values.
func (e *Error) Unwrap() error {
	return e.cause
}

func planningError(code ErrorCode, field, message string, cause error) *Error {
	return &Error{Code: code, Field: field, Message: message, cause: cause}
}
