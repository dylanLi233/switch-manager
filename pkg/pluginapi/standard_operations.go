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

	OperationRouteList   OperationName = "route.list"
	OperationRouteGet    OperationName = "route.get"
	OperationRouteCreate OperationName = "route.create"
	OperationRouteUpdate OperationName = "route.update"
	OperationRouteDelete OperationName = "route.delete"

	OperationACLList   OperationName = "acl.list"
	OperationACLGet    OperationName = "acl.get"
	OperationACLCreate OperationName = "acl.create"
	OperationACLUpdate OperationName = "acl.update"
	OperationACLDelete OperationName = "acl.delete"

	OperationMACTableList    OperationName = "mac_table.list"
	OperationARPTableList    OperationName = "arp_table.list"
	OperationDeviceStatusGet OperationName = "device_status.get"

	OperationCommandExecuteReadonly OperationName = "command.execute_readonly"
	OperationCommandExecuteConfig   OperationName = "command.execute_config"
)

// InterfaceNameValidator is an optional plugin extension. Interface syntax is
// vendor-specific and must not be guessed by the core service.
type InterfaceNameValidator interface {
	ValidateInterfaceName(string) error
}

// ACLNameValidator is an optional plugin extension. ACL naming syntax is
// vendor-specific and must not be guessed by the core service.
type ACLNameValidator interface {
	ValidateACLName(string) error
}
