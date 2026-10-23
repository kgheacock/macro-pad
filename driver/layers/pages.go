package layers

// A child layer with more than OnePage children has pages (task 0078). Page
// 1 keeps the up key on UpKey and shows the right arrow on RightKey. A later
// page shows the left arrow on LeftKey, and the right arrow on RightKey
// unless it is the last page. So every page but the last holds PerPage
// children, and the last holds up to OnePage.
const (
	// OnePage is the most children a child layer shows with no pages.
	OnePage = 5
	// PerPage is how many children a page with a right arrow holds.
	PerPage = 4
)

// childKeys are the keys a child layer shows children on, in order. A page
// with a right arrow uses the first PerPage of them.
var childKeys = [OnePage]int{0, 1, 2, 4, 5}

// PageCount returns how many pages a child layer of n children has: p pages
// hold PerPage*(p-1) + OnePage children.
func PageCount(n int) int {
	if n <= OnePage {
		return 1
	}
	return (n - 1 + PerPage - 1) / PerPage
}

// keyRole is what one key of a child layer page does.
type keyRole int

const (
	roleBlank keyRole = iota
	roleChild
	roleUp
	roleLeft
	roleRight
)

// pageKey is one key of a child layer page: its role, and for a child, the
// child's index.
type pageKey struct {
	role  keyRole
	child int
}

// pageLayout returns what each key shows on the given page of a child layer
// of n children.
func pageLayout(n, page int) [KeyCount]pageKey {
	var out [KeyCount]pageKey
	pages := PageCount(n)
	out[UpKey] = pageKey{role: roleUp}
	if page > 0 {
		out[LeftKey] = pageKey{role: roleLeft}
	}
	keys := childKeys[:]
	if page < pages-1 {
		out[RightKey] = pageKey{role: roleRight}
		keys = keys[:PerPage]
	}
	first := page * PerPage
	for j, k := range keys {
		if first+j >= n {
			break
		}
		out[k] = pageKey{role: roleChild, child: first + j}
	}
	return out
}

// clampPage keeps d.Page on a page the active layer has, after a change to
// its children, and at 0 on the top layer.
func (d *Doc) clampPage() {
	if d.Active == nil {
		d.Page = 0
		return
	}
	i := d.find(*d.Active)
	if i < 0 {
		d.Page = 0
		return
	}
	if last := PageCount(len(d.Keys[i].Children)) - 1; d.Page > last {
		d.Page = last
	}
	if d.Page < 0 {
		d.Page = 0
	}
}
