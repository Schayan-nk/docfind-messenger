package find

import (
	"fmt"
	"strings"
)

func logReportError(id string, err error) {
	fmt.Printf("report %s failed: %v\n", id, strings.TrimSpace(err.Error()))
}
