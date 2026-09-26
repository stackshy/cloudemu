package cloudformation

import (
	"encoding/base64"
	"math/big"
	"net/netip"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Fn::Cidr takes three arguments and makes at most 256 blocks, per the AWS
// reference page. Fn::GetAZs reports three zones per region.
const (
	cidrMaxCount = 256
	cidrArgs     = 3
	azCount      = 3
)

func templateErr(format string, args ...any) error {
	return cerrors.Newf(cerrors.InvalidArgument, "Template error: "+format, args...)
}

// argList returns arg as a list of exactly n items.
func argList(arg any, n int) ([]any, bool) {
	l, ok := arg.([]any)
	return l, ok && len(l) == n
}

// evalIf resolves Fn::If [condition, valueIfTrue, valueIfFalse].
func (r *Resolver) evalIf(arg any) (any, error) {
	branch, err := r.ifBranch(arg)
	if err != nil {
		return nil, err
	}

	return r.Resolve(branch)
}

// ifBranch returns the branch of an Fn::If the condition selects.
func (r *Resolver) ifBranch(arg any) (any, error) {
	const ifArgs = 3

	l, ok := argList(arg, ifArgs)
	if !ok {
		return nil, templateErr("Fn::If requires a list argument with three elements")
	}

	name := scalarString(l[0])

	val, known := r.conditions[name]
	if !known {
		return nil, templateErr("unresolved condition dependency %s in Fn::If", name)
	}

	if val {
		return l[1], nil
	}

	return l[2], nil
}

// findInMap resolves Fn::FindInMap [MapName, TopLevelKey, SecondLevelKey].
func (r *Resolver) findInMap(arg any) (any, error) {
	const findArgs = 3

	l, ok := argList(arg, findArgs)
	if !ok {
		return nil, templateErr("Fn::FindInMap requires a list argument with three elements")
	}

	keys := make([]string, findArgs)

	for i, e := range l {
		s, err := r.ResolveString(e)
		if err != nil {
			return nil, err
		}

		keys[i] = s
	}

	m, ok := r.mappings[keys[0]]
	if !ok {
		return nil, templateErr("Mapping named '%s' is not present in the 'Mappings' section of template.", keys[0])
	}

	v, ok := m[keys[1]][keys[2]]
	if !ok {
		return nil, templateErr("Unable to get mapping for %s::%s::%s", keys[0], keys[1], keys[2])
	}

	return v, nil
}

// selectFn resolves Fn::Select [index, list].
func (r *Resolver) selectFn(arg any) (any, error) {
	const selectArgs = 2

	l, ok := argList(arg, selectArgs)
	if !ok {
		return nil, templateErr("Fn::Select requires a list argument with two elements: an integer index and a list")
	}

	idxStr, err := r.ResolveString(l[0])
	if err != nil {
		return nil, err
	}

	idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
	if err != nil {
		return nil, templateErr("Fn::Select requires a list argument with two elements: an integer index and a list")
	}

	list, err := r.resolveToList(l[1], fnSelect)
	if err != nil {
		return nil, err
	}

	if idx < 0 || idx >= len(list) {
		return nil, templateErr("Fn::Select cannot select nonexistent value at index %d", idx)
	}

	return list[idx], nil
}

// resolveToList resolves node and requires a list result.
func (r *Resolver) resolveToList(node any, fn string) ([]any, error) {
	v, err := r.Resolve(node)
	if err != nil {
		return nil, err
	}

	list, ok := v.([]any)
	if !ok {
		return nil, templateErr("%s requires a list", fn)
	}

	return list, nil
}

// split resolves Fn::Split [delimiter, source].
func (r *Resolver) split(arg any) (any, error) {
	const splitArgs = 2

	l, ok := argList(arg, splitArgs)
	if !ok {
		return nil, templateErr("Fn::Split requires a list argument with two elements: a delimiter and a source string")
	}

	src, err := r.ResolveString(l[1])
	if err != nil {
		return nil, err
	}

	return stringList(strings.Split(src, scalarString(l[0]))), nil
}

// base64 resolves Fn::Base64 to the standard base64 encoding of its string.
func (r *Resolver) base64(arg any) (any, error) {
	s, err := r.ResolveString(arg)
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

// getAZs resolves Fn::GetAZs. An empty region means the stack's region. Each
// region reports three zones, the same rule the EC2 handler uses.
func (r *Resolver) getAZs(arg any) (any, error) {
	region, err := r.ResolveString(arg)
	if err != nil {
		return nil, err
	}

	if region == "" {
		region = r.Region
	}

	zones := make([]any, 0, azCount)
	for i := range azCount {
		zones = append(zones, region+string(rune('a'+i)))
	}

	return zones, nil
}

// cidr resolves Fn::Cidr [ipBlock, count, cidrBits]. Each result block has
// cidrBits host bits, so its prefix is 32 (IPv4) or 128 (IPv6) minus cidrBits.
func (r *Resolver) cidr(arg any) (any, error) {
	l, ok := argList(arg, cidrArgs)
	if !ok {
		return nil, templateErr("Fn::Cidr requires a list argument with three elements: an IP block, a count and CIDR bits")
	}

	vals := make([]string, cidrArgs)

	for i, e := range l {
		s, err := r.ResolveString(e)
		if err != nil {
			return nil, err
		}

		vals[i] = strings.TrimSpace(s)
	}

	block, err := netip.ParsePrefix(vals[0])
	if err != nil {
		return nil, templateErr("Fn::Cidr ipBlock %s is not a valid CIDR block", vals[0])
	}

	count, cErr := strconv.Atoi(vals[1])
	bits, bErr := strconv.Atoi(vals[2])

	if cErr != nil || bErr != nil || count < 1 || count > cidrMaxCount {
		return nil, templateErr("Fn::Cidr count must be between 1 and %d and cidrBits must be an integer", cidrMaxCount)
	}

	return splitCIDR(block.Masked(), count, bits)
}

// fitsBlocks reports whether 2^freeBits blocks cover count. count is at most
// 256, so nine free bits always fit.
func fitsBlocks(freeBits, count int) bool {
	const enough = 9

	return freeBits >= enough || 1<<freeBits >= count
}

// splitCIDR returns the first count blocks of the given host-bit size inside
// block.
func splitCIDR(block netip.Prefix, count, hostBits int) ([]any, error) {
	addrBits := block.Addr().BitLen()
	newPrefix := addrBits - hostBits

	if hostBits < 0 || newPrefix < block.Bits() || !fitsBlocks(newPrefix-block.Bits(), count) {
		return nil, templateErr("Fn::Cidr cannot fit %d blocks with %d CIDR bits in %s", count, hostBits, block)
	}

	base := new(big.Int).SetBytes(block.Addr().AsSlice())
	step := new(big.Int).Lsh(big.NewInt(1), uint(hostBits))
	out := make([]any, 0, count)

	for i := range count {
		n := new(big.Int).Add(base, new(big.Int).Mul(step, big.NewInt(int64(i))))

		buf := make([]byte, addrBits/8) //nolint:mnd // bits per byte
		n.FillBytes(buf)

		addr, _ := netip.AddrFromSlice(buf)
		out = append(out, netip.PrefixFrom(addr, newPrefix).String())
	}

	return out, nil
}
