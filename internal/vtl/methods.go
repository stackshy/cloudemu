package vtl

// Method names shared by more than one receiver type.
const (
	mIsEmpty  = "isEmpty"
	mContains = "contains"
	mIndexOf  = "indexOf"
	mSize     = "size"
	mGet      = "get"
	mRemove   = "remove"
	mSet      = "set"
	mPut      = "put"
	mKeySet   = "keySet"
	mValues   = "values"
	mEntrySet = "entrySet"
	mAddAll   = "addAll"
	mPutAll   = "putAll"
)

// pairArgs is the argument count of a two-argument method such as put or set.
const pairArgs = 2

type (
	listFn func(l *List, args []any) any
	mapFn  func(m *Map, args []any) any
)

// The Java-like method bridge for lists and maps.
//
//nolint:gochecknoglobals // read-only dispatch tables
var (
	listMethods = map[string]listFn{
		mSize:     func(l *List, _ []any) any { return int64(len(l.Items)) },
		mIsEmpty:  func(l *List, _ []any) any { return len(l.Items) == 0 },
		mGet:      listGet,
		"add":     listAdd,
		mAddAll:   listAddAll,
		mContains: func(l *List, args []any) any { return len(args) == 1 && listIndexOf(l, args[0]) >= 0 },
		mIndexOf:  listIndexOfMethod,
		mRemove:   listRemove,
		mSet:      listSet,
	}

	mapMethods = map[string]mapFn{
		mGet:          mapGet,
		mPut:          mapPut,
		mPutAll:       mapPutAll,
		"containsKey": mapContainsKey,
		mRemove:       func(m *Map, args []any) any { return m.Remove(Stringify(firstArg(args))) },
		mKeySet:       func(m *Map, _ []any) any { return stringList(m.keys) },
		mValues:       func(m *Map, _ []any) any { return NewList(iterItems(m, m.Len())...) },
		mEntrySet:     mapEntrySet,
		mSize:         func(m *Map, _ []any) any { return int64(m.Len()) },
		mIsEmpty:      func(m *Map, _ []any) any { return m.Len() == 0 },
	}
)

// creatingMethods are the list and map methods that return a new collection.
var creatingMethods = map[string]bool{mKeySet: true, mValues: true, mEntrySet: true} //nolint:gochecknoglobals // read-only

// callMethod dispatches a method call to the bridge for strings, lists and
// maps, or to a host Object, charging what the call creates against mem. An
// unknown method yields null, as Velocity does when no method matches.
func callMethod(mem *budget, v any, name string, args []any) (any, error) {
	switch {
	case name == "toString" && len(args) == 0:
		return chargeString(mem, v)
	case name == "equals" && len(args) == 1:
		return equal(v, args[0]), nil
	}

	switch t := v.(type) {
	case string:
		return callString(mem, t, name, args)
	case Object:
		return callObject(mem, t, name, args)
	default:
		return callCollection(mem, v, name, args)
	}
}

func chargeString(mem *budget, v any) (any, error) {
	s, err := format(v, MaxOutputBytes)
	if err != nil {
		return nil, err
	}

	return s, mem.charge(len(s))
}

func callString(mem *budget, s, name string, args []any) (any, error) {
	fn, ok := stringMethods[name]
	if !ok {
		return nil, nil
	}

	r, err := fn(mem, s, args)
	if err != nil {
		return nil, err
	}

	switch t := r.(type) {
	case string:
		if len(t) > MaxOutputBytes {
			return nil, ErrOutputLimit
		}

		return t, mem.charge(len(t))
	default:
		return r, nil
	}
}

// callCollection runs a list or map method, charging any growth of the
// receiver and any new collection it returns.
func callCollection(mem *budget, v any, name string, args []any) (any, error) {
	var (
		r      any
		before = collectionLen(v)
	)

	// Refuse growth that would not fit before the method allocates it.
	if err := mem.check(expectedGrowth(v, name, args) * slotBytes); err != nil {
		return nil, err
	}

	switch t := v.(type) {
	case *List:
		fn, ok := listMethods[name]
		if !ok {
			return nil, nil
		}

		r = fn(t, args)
	case *Map:
		fn, ok := mapMethods[name]
		if !ok {
			return nil, nil
		}

		r = fn(t, args)
	default:
		return nil, nil
	}

	grown := collectionLen(v) - before
	if creatingMethods[name] {
		grown += collectionLen(r)
	}

	if grown > 0 {
		if err := mem.charge(grown * slotBytes); err != nil {
			return nil, err
		}
	}

	return r, nil
}

// expectedGrowth estimates the entries a list or map method is about to add
// or build.
func expectedGrowth(v any, name string, args []any) int {
	switch {
	case name == mAddAll || name == mPutAll:
		return collectionLen(firstArg(args))
	case creatingMethods[name]:
		return collectionLen(v)
	default:
		return 1
	}
}

func collectionLen(v any) int {
	switch t := v.(type) {
	case *List:
		return len(t.Items)
	case *Map:
		return t.Len()
	default:
		return 0
	}
}

// callObject calls a host method. Its result is charged by size, since the
// host may build a new value from template-controlled input.
func callObject(mem *budget, o Object, name string, args []any) (any, error) {
	r, ok, err := o.Call(name, args)
	if err != nil || !ok {
		return nil, err
	}

	if s, isStr := r.(string); isStr && len(s) > MaxOutputBytes {
		return nil, ErrOutputLimit
	}

	return r, mem.charge(sizeOf(r, MaxAllocBytes))
}

// strArg returns argument i as a string; a non-string is stringified.
func strArg(args []any, i int) (string, bool) {
	if i >= len(args) || args[i] == nil {
		return "", false
	}

	return Stringify(args[i]), true
}

func intArg(args []any, i int) (int, bool) {
	if i >= len(args) {
		return 0, false
	}

	return toInt(args[i])
}

func firstArg(args []any) any {
	if len(args) == 0 {
		return nil
	}

	return args[0]
}

func listGet(l *List, args []any) any {
	v, _ := l.Index(intOr(args, -1))

	return v
}

func intOr(args []any, def int) int {
	if i, ok := intArg(args, 0); ok {
		return i
	}

	return def
}

func listAdd(l *List, args []any) any {
	if len(args) != 1 {
		return nil
	}

	l.Items = append(l.Items, args[0])

	return true
}

func listAddAll(l *List, args []any) any {
	other, ok := firstArg(args).(*List)
	if !ok {
		return false
	}

	l.Items = append(l.Items, other.Items...)

	return true
}

func listIndexOfMethod(l *List, args []any) any {
	if len(args) != 1 {
		return nil
	}

	return int64(listIndexOf(l, args[0]))
}

func listIndexOf(l *List, v any) int {
	for i, it := range l.Items {
		if equal(it, v) {
			return i
		}
	}

	return -1
}

// listRemove follows Java: remove(int) drops by index, remove(Object) by value.
func listRemove(l *List, args []any) any {
	if len(args) != 1 {
		return nil
	}

	if n, ok := args[0].(int64); ok {
		i, inRange := toInt(n)
		if !inRange || i < 0 || i >= len(l.Items) {
			return nil
		}

		prev := l.Items[i]
		l.Items = append(l.Items[:i], l.Items[i+1:]...)

		return prev
	}

	idx := listIndexOf(l, args[0])
	if idx < 0 {
		return false
	}

	l.Items = append(l.Items[:idx], l.Items[idx+1:]...)

	return true
}

func listSet(l *List, args []any) any {
	i, ok := intArg(args, 0)
	if !ok || len(args) != pairArgs || i < 0 || i >= len(l.Items) {
		return nil
	}

	prev := l.Items[i]
	l.Items[i] = args[1]

	return prev
}

func mapGet(m *Map, args []any) any {
	key, ok := strArg(args, 0)
	if !ok {
		return nil
	}

	v, _ := m.Get(key)

	return v
}

func mapPut(m *Map, args []any) any {
	if len(args) != pairArgs {
		return nil
	}

	return m.Put(Stringify(args[0]), args[1])
}

func mapPutAll(m *Map, args []any) any {
	if other, ok := firstArg(args).(*Map); ok {
		for _, k := range other.keys {
			m.Put(k, other.vals[k])
		}
	}

	return nil
}

func mapContainsKey(m *Map, args []any) any {
	key, ok := strArg(args, 0)
	if !ok {
		return false
	}

	_, found := m.Get(key)

	return found
}

func mapEntrySet(m *Map, _ []any) any {
	l := NewList()

	for _, k := range m.keys {
		entry := NewMap()
		entry.Put("key", k)
		entry.Put("value", m.vals[k])
		l.Items = append(l.Items, entry)
	}

	return l
}

func stringList(ss []string) *List {
	l := NewList()
	for _, s := range ss {
		l.Items = append(l.Items, s)
	}

	return l
}
