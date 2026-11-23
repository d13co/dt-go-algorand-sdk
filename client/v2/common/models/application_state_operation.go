package models

// ApplicationStateOperation an operation against an application's global/local/box
// state.
type ApplicationStateOperation struct {
	// Account for local state changes, the address of the account associated with the
	// local state.
	Account string `json:"account,omitempty"`

	// AppId the application whose state the operation changed: the running one,
	// unless a foreign box opcode (app_box_*) changes another application's
	// boxes. Only from a dt-algorand node asked with
	// SimulateTraceConfig.StateChangeAppId, and always in the traces of
	// algod.Client.GetTxnStateChangesForRound; 0 otherwise. dt-go-algorand-sdk:
	// not in upstream's spec, keep it when regenerating.
	AppId uint64 `json:"app-id,omitempty"`

	// AppStateType type of application state. Value `g` is **global state**, `l` is
	// **local state**, `b` is **boxes**.
	AppStateType string `json:"app-state-type"`

	// Key the key (name) of the global/local/box state.
	Key []byte `json:"key"`

	// NewValue represents an AVM value.
	NewValue AvmValue `json:"new-value,omitempty"`

	// Operation operation type. Value `w` is **write**, `d` is **delete**.
	Operation string `json:"operation"`
}
