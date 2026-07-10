package pluginapi

import "reflect"

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// IsNilPlugin detects nil and typed-nil plugin interfaces without invoking them.
func IsNilPlugin(plugin Plugin) bool { return isNilInterface(plugin) }

// IsNilCLISession detects nil and typed-nil session interfaces without invoking them.
func IsNilCLISession(session CLISession) bool { return isNilInterface(session) }
