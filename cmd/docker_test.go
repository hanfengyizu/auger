package cmd

import (
	"testing"
)

func TestSortKeysInt64s(t *testing.T) {
	arr := []int64{1, 2, 3, 4, 5, int64(^int64(0))}
	SortRevisionsDesc(arr)
	t.Log(arr)
}
