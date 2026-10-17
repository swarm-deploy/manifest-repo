package result

// Result is the versioned machine-readable outcome of a manifest operation.
type Result struct {
	// SchemaVersion identifies the structured result contract.
	SchemaVersion string `json:"schema_version"`
	// Operation is render or publish.
	Operation string `json:"operation"`
	// Changed reports whether the desired state changed semantically.
	Changed bool `json:"changed"`
	// Stack is the application stack name.
	Stack string `json:"stack"`
	// Target is the repository-relative desired-state path.
	Target string `json:"target"`
	// Changes contains semantic changes in stable object ID order.
	Changes []Change `json:"changes"`
	// Source contains source repository provenance when available.
	Source *Provenance `json:"source,omitempty"`
	// DesiredState contains desired-state repository provenance.
	DesiredState *Provenance `json:"desired_state,omitempty"`
}

// Provenance identifies a repository state involved in an operation.
type Provenance struct {
	// Repository identifies the repository.
	Repository string `json:"repository,omitempty"`
	// Revision identifies a Git revision.
	Revision string `json:"revision,omitempty"`
}

// Change describes one logical Compose object that changed.
type Change struct {
	// ID is the stable logical object ID.
	ID string `json:"id"`
	// Kind is the logical object kind.
	Kind string `json:"kind"`
	// Service identifies the parent service of an init job.
	Service string `json:"service,omitempty"`
	// Name is the logical object name.
	Name string `json:"name"`
	// Change is added, updated, or removed.
	Change string `json:"change"`
	// ChangedFields contains changed field names in stable order.
	ChangedFields []string `json:"changed_fields"`
	// Details contains before and after values only for safe fields.
	Details map[string]FieldChange `json:"details,omitempty"`
}

// FieldChange contains values for a field that is safe to disclose.
type FieldChange struct {
	// Before is the previous safe field value when present.
	Before any `json:"before,omitempty"`
	// After is the resulting safe field value when present.
	After any `json:"after,omitempty"`
}
