package fake

import "github.com/dylanLi233/switch-manager/pkg/pluginapi"

// validateInterfaceOutput keeps the shared parser call site small while
// delegating to the Fake plugin's vendor-specific interface-name validator.
func validateInterfaceOutput(operation pluginapi.OperationName, data any) error {
	return (&Plugin{}).validateInterfaceOutput(operation, data)
}
