package dp9ik

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"io"
	"math/big"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

var (
	fieldP = mustBig("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	fieldA = big.NewInt(1)
	fieldD = mod(big.NewInt(-39081))
	genX   = mustBig("297EA0EA2692FF1B4FAFF46098453A6A26ADF733245F065C3C59D0709CECFA96147EAAF3932D94C63D96C170033F4BA0C7F0DE840AED939F")
	genY   = big.NewInt(19)
	zero   = big.NewInt(0)
	one    = big.NewInt(1)
	two    = big.NewInt(2)
	four   = big.NewInt(4)
	halfP  = new(big.Int).Rsh(new(big.Int).Sub(fieldP, one), 1)
)

type extPoint struct {
	X *big.Int
	Y *big.Int
	Z *big.Int
	T *big.Int
}

func passtoaeskey(dst []byte, secret string) {
	derived := pbkdf2.Key([]byte(secret), []byte("Plan 9 key derivation"), 9001, AESKeyLen, sha1.New)
	copy(dst, derived)
}

func authpakHash(key *AuthKey, user string) {
	salt := sha256.Sum256([]byte(user))
	reader := hkdf.New(sha256.New, key.AES[:], salt[:], []byte("Plan 9 AuthPAK hash"))
	h := make([]byte, 2*PAKSLen)
	_, _ = io.ReadFull(reader, h)
	pm := spake2eeH2P(beToBig(h[:PAKSLen]))
	pn := spake2eeH2P(beToBig(h[PAKSLen:]))
	copy(key.PakHash[:PAKPLen], marshalPoint(pm))
	copy(key.PakHash[PAKPLen:], marshalPoint(pn))
}

func authpakNew(randReader io.Reader, key *AuthKey, isClient bool) (PAKState, []byte, error) {
	if randReader == nil {
		randReader = rand.Reader
	}
	x, err := rand.Int(randReader, fieldP)
	if err != nil {
		return PAKState{}, nil, err
	}
	base := loadPAKPoint(key.PakHash[:], isClient)
	y := spake2ee1(x, base)
	var state PAKState
	state.isClient = isClient
	copy(state.x[:], bigToBE(x, PAKXLen))
	copy(state.y[:], bigToBE(y, PAKYLen))
	return state, append([]byte(nil), state.y[:]...), nil
}

func authpakFinish(state *PAKState, key *AuthKey, peer []byte) error {
	if len(peer) != PAKYLen {
		return errors.New("invalid AuthPAK public key length")
	}
	base := loadPAKPoint(key.PakHash[:], !state.isClient)
	x := beToBig(state.x[:])
	z, err := spake2ee2(base, x, beToBig(peer))
	if err != nil {
		return err
	}
	saltInput := make([]byte, 0, 2*PAKYLen)
	if state.isClient {
		saltInput = append(saltInput, state.y[:]...)
		saltInput = append(saltInput, peer...)
	} else {
		saltInput = append(saltInput, peer...)
		saltInput = append(saltInput, state.y[:]...)
	}
	salt := sha256.Sum256(saltInput)
	reader := hkdf.New(sha256.New, bigToBE(z, PAKSLen), salt[:], []byte("Plan 9 AuthPAK key"))
	if _, err := io.ReadFull(reader, key.PakKey[:]); err != nil {
		return err
	}
	for i := range state.x {
		state.x[i] = 0
	}
	for i := range state.y {
		state.y[i] = 0
	}
	return nil
}

func spake2eeH2P(h *big.Int) *extPoint {
	n := new(big.Int).Set(two)
	minusOne := neg(one)
	for legendre(n).Cmp(minusOne) != 0 {
		n.Add(n, one)
	}
	return elligator2Point(n, mod(h))
}

func spake2ee1(x *big.Int, pHash *extPoint) *big.Int {
	scaled := edwardsScale(newExtPoint(genX, genY, one, modMul(genX, genY)), x)
	sum := edwardsAdd(scaled, pHash)
	return decafEncode(sum)
}

func spake2ee2(pHash *extPoint, x *big.Int, y *big.Int) (*big.Int, error) {
	point, err := decafDecode(y)
	if err != nil {
		return nil, err
	}
	negBase := &extPoint{
		X: neg(pHash.X),
		Y: clone(pHash.Y),
		Z: clone(pHash.Z),
		T: neg(pHash.T),
	}
	sum := edwardsAdd(point, negBase)
	scaled := edwardsScale(sum, x)
	return decafEncode(scaled), nil
}

func elligator2Point(n, r0 *big.Int) *extPoint {
	r := modMul(n, modMul(r0, r0))
	doubled := modAdd(fieldD, fieldD)

	D := modMul(
		modSub(modAdd(modMul(fieldD, r), modSub(fieldA, fieldD)), zero),
		modSub(modSub(modMul(fieldD, r), modMul(fieldA, r)), fieldD),
	)
	N := modMul(modAdd(r, one), modSub(fieldA, doubled))
	ND := modMul(N, D)

	var c, e *big.Int
	if ND.Sign() == 0 {
		c = clone(one)
		e = clone(zero)
	} else if sqrt := msqrt(ND); sqrt.Sign() != 0 {
		c = clone(one)
		e = modInv(sqrt)
	} else {
		c = neg(one)
		e = modMul(modMul(n, r0), misqrt(modMul(n, ND)))
	}

	s := modMul(modMul(c, N), e)
	t := neg(modAdd(modMul(
		modMul(modMul(c, N), modSub(r, one)),
		square(modMul(modSub(fieldA, doubled), e)),
	), one))

	X := modMul(modAdd(s, s), t)
	as2 := modMul(fieldA, square(s))
	Y := modMul(modSub(one, as2), modAdd(one, as2))
	Z := modMul(modAdd(one, as2), t)
	T := modMul(modAdd(s, s), modSub(one, as2))

	return newExtPoint(X, Y, Z, T)
}

func edwardsAdd(p1, p2 *extPoint) *extPoint {
	A := modMul(p1.X, p2.X)
	B := modMul(p1.Y, p2.Y)
	C := modMul(fieldD, modMul(p1.T, p2.T))
	D := modMul(p1.Z, p2.Z)
	E := modSub(modSub(modMul(modAdd(p1.X, p1.Y), modAdd(p2.X, p2.Y)), A), B)
	F := modSub(D, C)
	G := modAdd(D, C)
	H := modSub(B, modMul(fieldA, A))
	return newExtPoint(
		modMul(E, F),
		modMul(G, H),
		modMul(F, G),
		modMul(E, H),
	)
}

func edwardsScale(p *extPoint, s *big.Int) *extPoint {
	acc := newExtPoint(zero, one, one, zero)
	cur := clonePoint(p)
	k := new(big.Int).Set(s)
	for k.Sign() > 0 {
		if k.Bit(0) == 1 {
			acc = edwardsAdd(acc, cur)
		}
		cur = edwardsAdd(cur, cur)
		k.Rsh(k, 1)
	}
	return acc
}

func decafEncode(p *extPoint) *big.Int {
	r := misqrt(modMul(modMul(modSub(fieldA, fieldD), modAdd(p.Z, p.Y)), modSub(p.Z, p.Y)))
	u := modMul(modSub(fieldA, fieldD), r)
	r = decafNeg(neg(modMul(modAdd(u, u), p.Z)), r)
	s := modMul(
		modMul(u, modAdd(modMul(r, modSub(modMul(modMul(fieldA, p.Z), p.X), modMul(modMul(fieldD, p.Y), p.T))), p.Y)),
		modInv(fieldA),
	)
	return decafNeg(s, s)
}

func decafDecode(s *big.Int) (*extPoint, error) {
	if s.Cmp(halfP) > 0 {
		return nil, errors.New("invalid decaf point")
	}
	ss := square(s)
	Z := modAdd(modMul(fieldA, ss), one)
	u := modSub(square(Z), modMul(modMul(four, fieldD), ss))
	v := modMul(u, ss)

	var invSqrt *big.Int
	switch {
	case v.Sign() == 0:
		invSqrt = clone(one)
	default:
		root := msqrt(v)
		if root.Sign() == 0 {
			return nil, errors.New("invalid decaf point")
		}
		invSqrt = modInv(root)
	}

	v = decafNeg(modMul(u, invSqrt), invSqrt)
	w := modMul(modMul(v, s), modSub(two, Z))
	if s.Sign() == 0 {
		w = modAdd(w, one)
	}
	X := modAdd(s, s)
	Y := modMul(w, Z)
	T := modMul(w, X)
	return newExtPoint(X, Y, Z, T), nil
}

func legendre(a *big.Int) *big.Int {
	exp := new(big.Int).Rsh(new(big.Int).Sub(fieldP, one), 1)
	r := new(big.Int).Exp(mod(a), exp, fieldP)
	if r.Cmp(new(big.Int).Sub(fieldP, one)) == 0 {
		return neg(one)
	}
	return r
}

func msqrt(a *big.Int) *big.Int {
	a = mod(a)
	if a.Sign() == 0 {
		return clone(zero)
	}
	if legendre(a).Cmp(one) != 0 {
		return clone(zero)
	}

	s := new(big.Int).Sub(fieldP, one)
	e := 0
	for s.Bit(0) == 0 {
		s.Rsh(s, 1)
		e++
	}
	if e == 1 {
		exp := new(big.Int).Rsh(new(big.Int).Add(fieldP, one), 2)
		return new(big.Int).Exp(a, exp, fieldP)
	}

	n := clone(two)
	for legendre(n).Cmp(neg(one)) != 0 {
		n.Add(n, one)
	}
	x := new(big.Int).Exp(a, new(big.Int).Rsh(new(big.Int).Add(s, one), 1), fieldP)
	b := new(big.Int).Exp(a, s, fieldP)
	g := new(big.Int).Exp(n, s, fieldP)
	r := e
	for {
		m := 0
		t := clone(b)
		for m < r && t.Cmp(one) != 0 {
			t = square(t)
			m++
		}
		if m == 0 {
			return x
		}
		exp := 1 << (r - m - 1)
		gs := new(big.Int).Exp(g, big.NewInt(int64(exp)), fieldP)
		g = square(gs)
		x = modMul(x, gs)
		b = modMul(b, g)
		r = m
	}
}

func misqrt(a *big.Int) *big.Int {
	if new(big.Int).Mod(fieldP, four).Cmp(big.NewInt(3)) == 0 {
		exp := new(big.Int).Rsh(new(big.Int).Sub(fieldP, big.NewInt(3)), 2)
		return new(big.Int).Exp(mod(a), exp, fieldP)
	}
	root := msqrt(a)
	if root.Sign() == 0 {
		return root
	}
	return modInv(root)
}

func loadPAKPoint(hash []byte, isClient bool) *extPoint {
	offset := 0
	if !isClient {
		offset = PAKPLen
	}
	part := hash[offset : offset+PAKPLen]
	return &extPoint{
		X: beToBig(part[0*PAKSLen : 1*PAKSLen]),
		Y: beToBig(part[1*PAKSLen : 2*PAKSLen]),
		Z: beToBig(part[2*PAKSLen : 3*PAKSLen]),
		T: beToBig(part[3*PAKSLen : 4*PAKSLen]),
	}
}

func marshalPoint(p *extPoint) []byte {
	out := make([]byte, 0, PAKPLen)
	out = append(out, bigToBE(p.X, PAKSLen)...)
	out = append(out, bigToBE(p.Y, PAKSLen)...)
	out = append(out, bigToBE(p.Z, PAKSLen)...)
	out = append(out, bigToBE(p.T, PAKSLen)...)
	return out
}

func newExtPoint(x, y, z, t *big.Int) *extPoint {
	return &extPoint{X: mod(x), Y: mod(y), Z: mod(z), T: mod(t)}
}

func clonePoint(p *extPoint) *extPoint {
	return &extPoint{X: clone(p.X), Y: clone(p.Y), Z: clone(p.Z), T: clone(p.T)}
}

func clone(v *big.Int) *big.Int {
	return new(big.Int).Set(v)
}

func square(v *big.Int) *big.Int {
	return modMul(v, v)
}

func decafNeg(n, r *big.Int) *big.Int {
	if mod(n).Cmp(halfP) > 0 {
		return neg(r)
	}
	return mod(r)
}

func neg(v *big.Int) *big.Int {
	return mod(new(big.Int).Neg(v))
}

func mod(v *big.Int) *big.Int {
	out := new(big.Int).Mod(v, fieldP)
	if out.Sign() < 0 {
		out.Add(out, fieldP)
	}
	return out
}

func modAdd(a, b *big.Int) *big.Int {
	return mod(new(big.Int).Add(a, b))
}

func modSub(a, b *big.Int) *big.Int {
	return mod(new(big.Int).Sub(a, b))
}

func modMul(a, b *big.Int) *big.Int {
	return mod(new(big.Int).Mul(a, b))
}

func modInv(v *big.Int) *big.Int {
	return new(big.Int).ModInverse(mod(v), fieldP)
}

func beToBig(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}

func bigToBE(v *big.Int, size int) []byte {
	b := mod(v).Bytes()
	if len(b) >= size {
		out := make([]byte, size)
		copy(out, b[len(b)-size:])
		return out
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

func mustBig(hex string) *big.Int {
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic(hex)
	}
	return n
}
