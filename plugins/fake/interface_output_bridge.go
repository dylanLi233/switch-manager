package fake

import "github.com/dylanLi233/switch-manager/pkg/pluginapi"

// validateInterfaceOutput keeps the structured-result dispatcher generic while
// the plugin instance owns the vendor-specific interface-name validation.
func validateInterfaceOutput(plugin *Plugin, operation pluginapi.OperationName, data any) error {
	if plugin == nil {
		return pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "fake plugin is required")
	}
	return plugin.validateInterfaceOutput(operation, data)
}
