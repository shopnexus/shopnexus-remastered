package accountmodel

//go:generate go run shopnexus-server/cmd/genenum -type=Status,Role,Gender,AddressType

// Status is the account lifecycle state, decoupled from the DB enum.
type Status string

const (
	StatusActive    Status = "Active"
	StatusSuspended Status = "Suspended"
)

// Role is the account's platform role.
type Role string

const (
	RoleMember Role = "Member"
	RoleAdmin  Role = "Admin"
)

// Gender is the profile gender.
type Gender string

const (
	GenderMale   Gender = "Male"
	GenderFemale Gender = "Female"
	GenderOther  Gender = "Other"
)

// AddressType is the kind of a contact address.
type AddressType string

const (
	AddressTypeHome AddressType = "Home"
	AddressTypeWork AddressType = "Work"
)
