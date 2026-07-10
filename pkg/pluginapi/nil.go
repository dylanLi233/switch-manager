package pluginapi

import "reflect"

// IsNilPlugin detects nil and typed-nil plugin interfaces without invoking them.
func IsNilPlugin(plugin Plugin) bool {
	if plugin == nil {
		return true
	}
	value := reflect.ValueOf(plugin)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
