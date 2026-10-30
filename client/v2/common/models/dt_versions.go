package models

// This file is part of go-dtalgorand-sdk: the responses of the /versions
// endpoints of a time-travel algod. Unlike the generated models, fields that
// the node may leave out where zero means something else are pointers.

// AccountVersionsResponse is a page of the versions of an account, or of its
// holding of an asset or local state of an application.
type AccountVersionsResponse struct {
	// NextToken is the token for the next page, absent on the last one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the last round of the range served, or with rounds, the largest
	// requested round left.
	Round uint64 `json:"round"`

	// Versions lists the rounds at which something changed, or with rounds, the
	// state as of each requested round.
	Versions []AccountVersion `json:"versions"`
}

// AccountVersion is what changed in an account at one round.
type AccountVersion struct {
	// AppsLocalState lists the local states that changed.
	AppsLocalState []ApplicationLocalStateVersion `json:"apps-local-state,omitempty"`

	// Assets lists the asset holdings that changed.
	Assets []AssetHoldingVersion `json:"assets,omitempty"`

	// Base is the account's base record, present if it changed.
	Base *AccountBaseVersion `json:"base,omitempty"`

	// CreatedApps lists the params of the created applications that changed.
	CreatedApps []CreatedApplicationVersion `json:"created-apps,omitempty"`

	// CreatedAssets lists the params of the created assets that changed.
	CreatedAssets []CreatedAssetVersion `json:"created-assets,omitempty"`

	// Round is the round of the version.
	Round uint64 `json:"round"`

	// Timestamp is the block's timestamp, in seconds since the epoch.
	Timestamp int64 `json:"timestamp"`
}

// AccountBaseVersion is a version of an account's base record.
type AccountBaseVersion struct {
	// Account is the account as of the version, with include=values. Its
	// resources are not set (as with exclude=all).
	Account *Account `json:"account,omitempty"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the account was closed.
	Deleted bool `json:"deleted"`
}

// AssetHoldingVersion is a version of an account's holding of an asset.
type AssetHoldingVersion struct {
	// AssetHolding is the holding as of the version, with include=values.
	AssetHolding *AssetHolding `json:"asset-holding,omitempty"`

	// AssetId is the asset's ID.
	AssetId uint64 `json:"asset-id"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the account opted out.
	Deleted bool `json:"deleted"`
}

// ApplicationLocalStateVersion is a version of an account's local state of
// an application.
type ApplicationLocalStateVersion struct {
	// AppId is the application's ID.
	AppId uint64 `json:"app-id"`

	// AppLocalState is the local state as of the version, with include=values.
	AppLocalState *ApplicationLocalState `json:"app-local-state,omitempty"`

	// ChangedKeys lists the keys that changed, with include=keys. It is nil
	// when the node did not list them, and empty when no key changed.
	ChangedKeys *[][]byte `json:"changed-keys,omitempty"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the account closed out.
	Deleted bool `json:"deleted"`

	// KeyValue is the value of the requested key, with key and include=values;
	// nil when the key is not set.
	KeyValue *TealValue `json:"key-value,omitempty"`
}

// CreatedAssetVersion is a version of a created asset's params.
type CreatedAssetVersion struct {
	// Asset is the asset as of the version, with include=values.
	Asset *Asset `json:"asset,omitempty"`

	// AssetId is the asset's ID.
	AssetId uint64 `json:"asset-id"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the asset was destroyed.
	Deleted bool `json:"deleted"`
}

// CreatedApplicationVersion is a version of a created application's params.
type CreatedApplicationVersion struct {
	// AppId is the application's ID.
	AppId uint64 `json:"app-id"`

	// Application is the application as of the version, with include=values.
	Application *Application `json:"application,omitempty"`

	// ChangedKeys lists the global state keys that changed, with include=keys.
	// It is nil when the node did not list them, and empty when no key changed.
	ChangedKeys *[][]byte `json:"changed-keys,omitempty"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the application was deleted.
	Deleted bool `json:"deleted"`
}

// AssetVersionsResponse is a page of the versions of an asset's params.
type AssetVersionsResponse struct {
	// NextToken is the token for the next page, absent on the last one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the last round of the range served, or with rounds, the largest
	// requested round left.
	Round uint64 `json:"round"`

	// Versions lists the versions.
	Versions []AssetVersion `json:"versions"`
}

// AssetVersion is a version of an asset's params.
type AssetVersion struct {
	// Asset is the asset as of the version, with include=values.
	Asset *Asset `json:"asset,omitempty"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the asset was destroyed (or, with rounds, did not
	// exist).
	Deleted bool `json:"deleted"`

	// Round is the round of the version.
	Round uint64 `json:"round"`

	// Timestamp is the block's timestamp, in seconds since the epoch.
	Timestamp int64 `json:"timestamp"`
}

// ApplicationVersionsResponse is a page of the versions of an application's
// params.
type ApplicationVersionsResponse struct {
	// NextToken is the token for the next page, absent on the last one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the last round of the range served, or with rounds, the largest
	// requested round left.
	Round uint64 `json:"round"`

	// Versions lists the versions.
	Versions []ApplicationVersion `json:"versions"`
}

// ApplicationVersion is a version of an application's params, global state
// included.
type ApplicationVersion struct {
	// Application is the application as of the version, with include=values.
	Application *Application `json:"application,omitempty"`

	// ChangedKeys lists the global state keys that changed, with include=keys.
	// It is nil when the node did not list them, and empty when no key changed.
	ChangedKeys *[][]byte `json:"changed-keys,omitempty"`

	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the application was deleted (or, with rounds, did not
	// exist).
	Deleted bool `json:"deleted"`

	// KeyValue is the value of the requested key, with key and include=values;
	// nil when the key is not set.
	KeyValue *TealValue `json:"key-value,omitempty"`

	// Round is the round of the version.
	Round uint64 `json:"round"`

	// Timestamp is the block's timestamp, in seconds since the epoch.
	Timestamp int64 `json:"timestamp"`
}

// BoxVersionsResponse is a page of the versions of a box.
type BoxVersionsResponse struct {
	// NextToken is the token for the next page, absent on the last one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the last round of the range served, or with rounds, the largest
	// requested round left.
	Round uint64 `json:"round"`

	// Versions lists the versions.
	Versions []BoxVersion `json:"versions"`
}

// BoxVersion is a version of a box.
type BoxVersion struct {
	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the box was deleted (or, with rounds, did not exist).
	Deleted bool `json:"deleted"`

	// Round is the round of the version.
	Round uint64 `json:"round"`

	// Timestamp is the block's timestamp, in seconds since the epoch.
	Timestamp int64 `json:"timestamp"`

	// Value is the box's value, with include=values. It is nil when not
	// requested or deleted, and can be empty.
	Value *[]byte `json:"value,omitempty"`
}

// BoxesVersionsResponse is a page of the rounds at which boxes of an
// application changed.
type BoxesVersionsResponse struct {
	// NextToken is the token for the next page, absent on the last one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the last round of the range served, or with rounds, the largest
	// requested round left.
	Round uint64 `json:"round"`

	// Versions lists the rounds and the boxes that changed in each.
	Versions []BoxesVersion `json:"versions"`
}

// BoxesVersion lists the boxes of an application that changed at a round,
// or with rounds, the boxes as of that round.
type BoxesVersion struct {
	// Boxes lists the boxes.
	Boxes []BoxChange `json:"boxes"`

	// Round is the round of the version.
	Round uint64 `json:"round"`

	// Timestamp is the block's timestamp, in seconds since the epoch.
	Timestamp int64 `json:"timestamp"`
}

// BoxChange is a version of one box, within a BoxesVersion.
type BoxChange struct {
	// ChangedRound is the round it last changed, with rounds.
	ChangedRound *uint64 `json:"changed-round,omitempty"`

	// Deleted is whether the box was deleted.
	Deleted bool `json:"deleted"`

	// Name is the box's name.
	Name []byte `json:"name"`

	// Value is the box's value, with include=values. It is nil when not
	// requested or deleted, and can be empty.
	Value *[]byte `json:"value,omitempty"`
}
