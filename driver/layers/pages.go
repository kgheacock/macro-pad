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

// settle keeps the active layer and its pages valid after a change to the
// tree. While the active node is gone or has no children, the pad goes up
// one level, to the page that layer showed (task 0086). Then every page on
// the path is clamped to its layer, and the top layer has page 0.
func (d *Doc) settle() {
	for len(d.Active) > 0 {
		if n := d.open(); n != nil && len(n.Children) > 0 {
			break
		}
		d.up()
	}
	if len(d.Active) == 0 {
		d.Active, d.Page, d.ParentPages = nil, 0, nil
		return
	}
	for len(d.ParentPages) < len(d.Active)-1 {
		d.ParentPages = append(d.ParentPages, 0)
	}
	d.ParentPages = d.ParentPages[:len(d.Active)-1]
	for i := range d.ParentPages {
		d.ParentPages[i] = clamp(d.ParentPages[i], len(d.nodeAt(d.Active[:i+1]).Children))
	}
	d.Page = clamp(d.Page, len(d.open().Children))
}

// up closes the active layer and shows the one it was opened from, on the
// page it showed then.
func (d *Doc) up() {
	d.Active = d.Active[:len(d.Active)-1]
	d.Page = 0
	if n := len(d.ParentPages); n > 0 {
		d.Page = d.ParentPages[n-1]
		d.ParentPages = d.ParentPages[:n-1]
	}
	if len(d.Active) == 0 {
		d.Active, d.Page, d.ParentPages = nil, 0, nil
	}
}

// clamp keeps page on a page a layer of n children has.
func clamp(page, n int) int {
	if last := PageCount(n) - 1; page > last {
		page = last
	}
	if page < 0 {
		page = 0
	}
	return page
}
