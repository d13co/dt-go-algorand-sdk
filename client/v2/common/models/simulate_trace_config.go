package models

// SimulateTraceConfig an object that configures simulation execution trace.
type SimulateTraceConfig struct {
	// Enable a boolean option for opting in execution trace features simulation
	// endpoint.
	Enable bool `json:"enable,omitempty"`

	// ScratchChange a boolean option enabling returning scratch slot changes together
	// with execution trace during simulation.
	ScratchChange bool `json:"scratch-change,omitempty"`

	// StackChange a boolean option enabling returning stack changes together with
	// execution trace during simulation.
	StackChange bool `json:"stack-change,omitempty"`

	// StateChange a boolean option enabling returning application state changes
	// (global, local, and box changes) with the execution trace during simulation.
	StateChange bool `json:"state-change,omitempty"`

	// StateChangeAppId a boolean option adding AppId to each application state
	// change of the execution trace. Needs StateChange. dt-go-algorand-sdk: not in
	// upstream's spec, keep it when regenerating. Stock algod and dt-algorand
	// nodes before dt6 refuse it with 400: check
	// algod.Client.SupportsStateChangeAppId first.
	StateChangeAppId bool `json:"state-change-app-id,omitempty"`
}
