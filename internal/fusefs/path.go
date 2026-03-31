package fusefs

import "path"

func JoinPath(base, name string) string {
	if base == "/" {
		return path.Clean("/" + name)
	}
	return path.Clean(base + "/" + name)
}
