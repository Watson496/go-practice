// Package httpboundary はHTTP入口で共有する検査を提供する。
package httpboundary

import "strings"

// ValidURLPath は、配信ルートの外を指す区間を持たないURLパスかを返す。
func ValidURLPath(urlPath string) bool {
	if strings.Contains(urlPath, `\`) {
		return false
	}
	for _, segment := range strings.Split(urlPath, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
