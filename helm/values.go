package helm

import (
	"fmt"
	"strings"
)

// FlattenChartValues converts helm nested values into flat key value structure.
func FlattenChartValues(values map[string]interface{}) map[string]string {
	res := map[string]string{}

	// stack is used for storing path and iterate DFS.
	var stack []string

	var dfs func(root map[string]interface{})
	dfs = func(root map[string]interface{}) {
		for k, v := range root {
			stack = append(stack, k)

			children, ok := v.(map[string]interface{})
			if ok {
				dfs(children)
			} else {
				path := strings.Join(stack, ".")
				res[path] = fmt.Sprintf("%v", v)

				// Pop last path element from path.
				stack = stack[:len(stack)-1]
			}
		}

		// Pop last element from path.
		if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	}

	// Traverse all values.
	dfs(values)

	return res
}
