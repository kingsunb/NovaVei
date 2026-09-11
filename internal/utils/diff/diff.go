// diff.go provides set-difference helpers over ordered, comparable slices.
package diff

// Ordered is the set of numeric/string element types supported by DiffSets.
type Ordered interface {
	~string | ~byte | ~int | ~int8 | ~int16 | ~int64 | ~float32 | ~float64
}

// DiffSets compares old and new multiset slices and returns the elements
// deleted from old and added in new.
func DiffSets[T Ordered](old []T, new []T) (deleted []T, added []T) {
	m := make(map[T]int, len(old))
	for _, v := range old {
		m[v]++
	}
	for _, v := range new {
		if m[v] > 0 {
			m[v]--
		} else {
			added = append(added, v)
		}
	}
	for k, c := range m {
		for i := 0; i < c; i++ {
			deleted = append(deleted, k)
		}
	}
	return
}
