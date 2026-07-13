package pluginapi

const (
	OperationVLANList   OperationName = "vlan.list"
	OperationVLANGet    OperationName = "vlan.get"
	OperationVLANCreate OperationName = "vlan.create"
	OperationVLANUpdate OperationName = "vlan.update"
	OperationVLANDelete OperationName = "vlan.delete"

	OperationInterfaceList       OperationName = "interface.list"
	OperationInterfaceGet        OperationName = "interface.get"
	OperationInterfaceEnable     OperationName = "interface.enable"
	OperationInterfaceDisable    OperationName = "interface.disable"
	OperationInterfaceAccess     OperationName = "interface.access"
	OperationInterfaceTrunk      OperationName = "interface.trunk"
	OperationInterfaceVLANAdd    OperationName = "interface.vlan.add"
	OperationInterfaceVLANRemove OperationName = "interface.vlan.remove"
)

// InterfaceNameValidator is an optional plugin extension. Interface syntax is
// vendor-specific and must not be guessed by the core service.
type InterfaceNameValidator interface {
	ValidateInterfaceName(string) error
}
