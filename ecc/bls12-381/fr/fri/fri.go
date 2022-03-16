// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by consensys/gnark-crypto DO NOT EDIT

package fri

import (
	"errors"
	"fmt"
	"hash"
	"math/big"
	"math/bits"

	"github.com/consensys/gnark-crypto/accumulator/merkletree"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr/fft"
	fiatshamir "github.com/consensys/gnark-crypto/fiat-shamir"
)

var (
	ErrProximityTest = errors.New("fri proximity test failed")
	ErrOddSize       = errors.New("the size should be even")
	ErrMerkleRoot    = errors.New("merkle roots of the opening and the proof of proximity don't coincide")
	ErrMerklePath    = errors.New("merkle path proof is wrong")
	ErrRangePosition = errors.New("the asked opening position is out of range")
)

const rho = 8

// Digest commitment of a polynomial.
type Digest []byte

// merkleProof helper structure to build the merkle proof
// It is called partial Merkle tree because at each round, 2 contiguous values
// are queried, so only the Merkle paths corresponding to the 2 values are
// the same except for the leafs. So during an round of interaction, one Merkle path
// proof will contain every hashes, while the Merkle path proof for the neighbor value
// will only contain the leafs. The Merkle path proof is similar after the leafs
// Technically in the second Merkle path proof, proofSet will contain only 2 elements,
// the neighbor value and the hash of the value from the first Merkle path proof.
type partialMerkleProof struct {
	merkleRoot []byte
	proofSet   [][]byte
	numLeaves  uint64
}

// MerkleProof used to open a polynomial
type OpeningProof struct {
	merkleRoot   []byte
	proofSet     [][]byte
	numLeaves    uint64
	index        uint64
	ClaimedValue fr.Element
}

// IOPP Interactive Oracle Proof of Proximity
type IOPP uint

const (
	RADIX_2_FRI IOPP = iota
)

// ProofOfProximity proof of proximity, attesting that
// a function is d-close to a low degree polynomial.
type ProofOfProximity struct {

	// stores the interactions between the prover and the verifier.
	// Each interaction results in a set or merkle proofs, corresponding
	// to the queries of the verifier.
	interactions [][2]partialMerkleProof

	// evaluation stores the evaluation of the fully folded polynomial.
	// The verifier need to reconstruct the polynomial, and check that
	// it is low degree.
	evaluation []fr.Element
}

// Iopp interface that an iopp should implement
type Iopp interface {

	// BuildProofOfProximity creates a proof of proximity that p is d-close to a polynomial
	// of degree len(p). The proof is built non interactively using Fiat Shamir.
	BuildProofOfProximity(p []fr.Element) (ProofOfProximity, error)

	// VerifyProofOfProximity verifies the proof of proximity. It returns an error if the
	// verification fails.
	VerifyProofOfProximity(proof ProofOfProximity) error

	// Opens a polynomial at gⁱ where i = position.
	Open(p []fr.Element, position uint64) (OpeningProof, error)

	// Verifies the opening of a polynomial at gⁱ where i = position.
	VerifyOpening(position uint64, openingProof OpeningProof, pp ProofOfProximity) error
}

// GetRho returns the factor size_code_word/size_polynomial
func GetRho() int {
	return rho
}

// New creates a new IOPP capable to handle degree(size) polynomials.
func (iopp IOPP) New(size uint64, h hash.Hash) Iopp {
	switch iopp {
	case RADIX_2_FRI:
		return newRadixTwoFri(size, h)
	default:
		panic("iopp name is not recognized")
	}
}

// radixTwoFri empty structs implementing compressionFunction for
// the squaring function.
type radixTwoFri struct {

	// hash function that is used for Fiat Shamir and for committing to
	// the oracles.
	h hash.Hash

	// nbSteps number of interactions between the prover and the verifier
	nbSteps int

	// domains list of domains used for fri
	// TODO normally, a single domain of size n=2ᶜ should handle all
	// polynomials of size 2ᵈ where d<c...
	domains []*fft.Domain
}

func newRadixTwoFri(size uint64, h hash.Hash) radixTwoFri {

	var res radixTwoFri

	// computing the number of steps
	n := ecc.NextPowerOfTwo(size)
	nbSteps := bits.TrailingZeros(uint(n))
	res.nbSteps = nbSteps

	// extending the domain
	n = n * rho

	// building the domains
	res.domains = make([]*fft.Domain, nbSteps)
	for i := 0; i < nbSteps; i++ {
		res.domains[i] = fft.NewDomain(n)
		n = n >> 1
	}

	// hash function
	res.h = h

	return res
}

// finds i such that gⁱ = a
// TODO for the moment assume it exits and easily computable
func (s radixTwoFri) log(a, g fr.Element) int {
	var i int
	var _g fr.Element
	_g.SetOne()
	for i = 0; ; i++ {
		if _g.Equal(&a) {
			break
		}
		_g.Mul(&_g, &g)
	}
	return i
}

// convertOrderCanonical convert the index i, an entry in a
// sorted polynomial, to the corresponding entry in canonical
// representation. n is the size of the polynomial.
func convertSortedCanonical(i, n int) int {
	if i%2 == 0 {
		return i / 2
	} else {
		l := (n - 1 - i) / 2
		return n - 1 - l
	}
}

// convertCanonicalSorted convert the index i, an entry in a
// sorted polynomial, to the corresponding entry in canonical
// representation. n is the size of the polynomial.
func convertCanonicalSorted(i, n int) int {

	if i < n/2 {
		return 2 * i
	} else {
		l := n - (i + 1)
		l = 2 * l
		return n - l - 1
	}

}

// deriveQueriesPositions derives the indices of the oracle
// function that the verifier has to pick. The result is a
// slice of []int, where each entry is a tuple (i_k), such that
// the verifier needs to evaluate sum_k oracle(i_k)xᵏ to build
// the folded function.
func (s radixTwoFri) deriveQueriesPositions(a fr.Element) []int {

	res := make([]int, s.nbSteps+1)

	l := s.log(a, s.domains[0].Generator)
	n := int(s.domains[0].Cardinality)

	// first we convert from canonical indexation to sorted indexation
	for i := 0; i < s.nbSteps+1; i++ {

		// canonical → sorted
		if l < n/2 {
			res[i] = 2 * l
		} else {
			res[i] = (n - 1) - 2*(n-1-l)
			l = l - n/2
		}
		n = n >> 1
	}

	return res
}

// sort orders the evaluation of a polynomial on a domain
// such that contiguous entries are in the same fiber:
// {q(g⁰), q(g^{n/2}), q(g¹), q(g^{1+n/2}),...,q(g^{n/2-1}), q(gⁿ⁻¹)}
func sort(evaluations []fr.Element) []fr.Element {
	q := make([]fr.Element, len(evaluations))
	n := len(evaluations) / 2
	for i := 0; i < len(evaluations)/2; i++ {
		q[2*i].Set(&evaluations[i])
		q[2*i+1].Set(&evaluations[i+n])
	}
	return q
}

// Opens a polynomial at gⁱ where i = position.
func (s radixTwoFri) Open(p []fr.Element, position uint64) (OpeningProof, error) {

	// check that position is in the correct range
	if position >= s.domains[0].Cardinality {
		return OpeningProof{}, ErrRangePosition
	}

	// put q in evaluation form
	q := make([]fr.Element, s.domains[0].Cardinality)
	copy(q, p)
	s.domains[0].FFT(q, fft.DIF)
	fft.BitReverse(q)

	// sort q to have fibers in contiguous entries
	q = sort(q)

	// build the Merkle proof, we the position is converted to fit the sorted polynomial
	pos := convertCanonicalSorted(int(position), len(q))

	tree := merkletree.New(s.h)
	err := tree.SetIndex(uint64(pos))
	if err != nil {
		return OpeningProof{}, err
	}
	for i := 0; i < len(q); i++ {
		tree.Push(q[i].Marshal())
	}
	var res OpeningProof
	res.merkleRoot, res.proofSet, res.index, res.numLeaves = tree.Prove()

	// set the claimed value, which is the first entry of the Merkle proof
	res.ClaimedValue.SetBytes(res.proofSet[0])

	return res, nil
}

func checkRoots(a, b []byte) error {

	if len(a) != len(b) {
		return ErrMerkleRoot
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return ErrMerkleRoot
		}
	}

	return nil
}

// Verifies the opening of a polynomial.
// * position the point at which the proof is opened (the point is g^{i} where i = position)
// * openingProof Merkle path proof
// * pp proof of proximity, needed because before opening Merkle path proof one should be sure that the
// committed values come from a polynomial. During the verification of the Merkle path proof, the root
// hash of the Merkle path is compared to the root hash of the first interaction of the proof of proximity,
// those should be equal, if not an error is raised.
func (s radixTwoFri) VerifyOpening(position uint64, openingProof OpeningProof, pp ProofOfProximity) error {

	// index containing the full Merkle proof
	var fullMerkleProof int
	if len(pp.interactions[0][0].proofSet) > len(pp.interactions[0][1].proofSet) {
		fullMerkleProof = 0
	} else {
		fullMerkleProof = 1
	}

	// check that the merkle roots coincide
	err := checkRoots(openingProof.merkleRoot, pp.interactions[0][fullMerkleProof].merkleRoot)
	if err != nil {
		return err
	}

	// convert position to the sorted version
	sizePoly := s.domains[0].Cardinality
	pos := convertCanonicalSorted(int(position), int(sizePoly))

	// check the Merkle proof
	res := merkletree.VerifyProof(s.h, openingProof.merkleRoot, openingProof.proofSet, uint64(pos), openingProof.numLeaves)
	if !res {
		return ErrMerklePath
	}
	return nil

}

// BuildProofOfProximity generates a proof that a function, given as an oracle from
// the verifier point of view, is in fact d-close to a polynomial.
func (s radixTwoFri) BuildProofOfProximity(p []fr.Element) (ProofOfProximity, error) {

	extendedSize := int(s.domains[0].Cardinality)
	_p := make([]fr.Element, extendedSize)
	copy(_p, p)

	// the proof will contain nbSteps interactions
	var proof ProofOfProximity
	proof.interactions = make([][2]partialMerkleProof, s.nbSteps)

	// Fiat Shamir transcript to derive the challenges
	xis := make([]string, s.nbSteps+1)
	for i := 0; i < s.nbSteps; i++ {
		xis[i] = fmt.Sprintf("x%d", i)
	}
	xis[s.nbSteps] = "s0"
	fs := fiatshamir.NewTranscript(s.h, xis...)

	// step 1 : fold the polynomial using the xi
	evalsAtRound := make([][]fr.Element, s.nbSteps)

	for i := 0; i < s.nbSteps; i++ {

		// evaluate _p and sort the result
		s.domains[i].FFT(_p, fft.DIF)
		fft.BitReverse(_p)
		evalsAtRound[i] = sort(_p)

		// compute the root hash, needed to derive xi
		t := merkletree.New(s.h)
		for k := 0; k < len(evalsAtRound[i]); k++ {
			t.Push(evalsAtRound[i][k].Marshal())
		}
		rh := t.Root()
		err := fs.Bind(xis[i], rh)
		if err != nil {
			return proof, err
		}

		// derive the challenge
		bxi, err := fs.ComputeChallenge(xis[i])
		if err != nil {
			return proof, err
		}
		var xi fr.Element
		xi.SetBytes(bxi)

		// put _p back in canonical basis
		s.domains[i].FFTInverse(_p, fft.DIF)
		fft.BitReverse(_p)

		// fold _p
		fp := make([]fr.Element, len(_p)/2)
		for k := 0; k < len(_p)/2; k++ {
			fp[k].Mul(&_p[2*k+1], &xi)
			fp[k].Add(&fp[k], &_p[2*k])
		}

		_p = fp
	}

	// last round, provide the evaluation
	proof.evaluation = make([]fr.Element, len(_p))
	var g fr.Element
	g.SetOne()
	for i := 0; i < rho; i++ {
		e := eval(_p, g)
		proof.evaluation[i].Set(&e)
		g.Mul(&g, &s.domains[s.nbSteps-1].Generator)
	}

	// step 2: provide the Merkle proofs of the queries

	// derive the verifier queries
	for i := 0; i < len(proof.evaluation); i++ {
		err := fs.Bind(xis[s.nbSteps], proof.evaluation[i].Marshal())
		if err != nil {
			return proof, err
		}
	}
	binSeed, err := fs.ComputeChallenge(xis[s.nbSteps])
	if err != nil {
		return proof, err
	}
	var bigSeed big.Int
	bigSeed.SetBytes(binSeed[:s.nbSteps/8+1]) // only the first nbSteps bits are relevant
	var gen fr.Element
	gen.Exp(s.domains[0].Generator, &bigSeed)
	si := s.deriveQueriesPositions(gen)

	for i := 0; i < s.nbSteps; i++ {

		// build proofs of queries at s[i]
		t := merkletree.New(s.h)
		err := t.SetIndex(uint64(si[i]))
		if err != nil {
			return proof, err
		}
		for k := 0; k < len(evalsAtRound[i]); k++ {
			t.Push(evalsAtRound[i][k].Marshal())
		}
		mr, proofSet, _, numLeaves := t.Prove()
		c := si[i] % 2
		proof.interactions[i][c] = partialMerkleProof{mr, proofSet, numLeaves}
		proof.interactions[i][1-c] = partialMerkleProof{
			mr,
			make([][]byte, 2),
			numLeaves,
		}
		proof.interactions[i][1-c].proofSet[0] = evalsAtRound[i][si[i]+1-2*c].Marshal()
		s.h.Reset()
		_, err = s.h.Write(proof.interactions[i][c].proofSet[0])
		if err != nil {
			return proof, err
		}
		proof.interactions[i][1-c].proofSet[1] = s.h.Sum(nil)

	}

	return proof, nil
}

// VerifyProofOfProximity verifies the proof of proximity. It returns an error if the
// verification fails.
func (s radixTwoFri) VerifyProofOfProximity(proof ProofOfProximity) error {

	// Fiat Shamir transcript to derive the challenges
	xis := make([]string, s.nbSteps+1)
	for i := 0; i < s.nbSteps; i++ {
		xis[i] = fmt.Sprintf("x%d", i)
	}
	xis[s.nbSteps] = "s0"
	fs := fiatshamir.NewTranscript(s.h, xis...)
	xi := make([]fr.Element, s.nbSteps)

	for i := 0; i < s.nbSteps; i++ {
		err := fs.Bind(xis[i], proof.interactions[i][0].merkleRoot)
		if err != nil {
			return err
		}
		bxi, err := fs.ComputeChallenge(xis[i])
		if err != nil {
			return err
		}
		xi[i].SetBytes(bxi)
	}

	// derive the verifier queries
	for i := 0; i < len(proof.evaluation); i++ {
		err := fs.Bind(xis[s.nbSteps], proof.evaluation[i].Marshal())
		if err != nil {
			return err
		}
	}
	binSeed, err := fs.ComputeChallenge(xis[s.nbSteps])
	if err != nil {
		return err
	}
	var bigSeed big.Int
	bigSeed.SetBytes(binSeed[:s.nbSteps/8+1]) // only the first nbSteps bits are relevant
	var gen fr.Element
	gen.Exp(s.domains[0].Generator, &bigSeed)
	si := s.deriveQueriesPositions(gen)

	// for each round check the Merkle proof and the correctness of the folding
	var twoInv fr.Element
	twoInv.SetUint64(2).Inverse(&twoInv)
	for i := 0; i < len(proof.interactions); i++ {

		// correctness of Merkle proof
		c := si[i] % 2
		res := merkletree.VerifyProof(
			s.h,
			proof.interactions[i][c].merkleRoot,
			proof.interactions[i][c].proofSet,
			uint64(si[i]),
			proof.interactions[i][c].numLeaves,
		)
		if !res {
			return ErrProximityTest
		}

		proofSet := make([][]byte, len(proof.interactions[i][c].proofSet))
		copy(proofSet[2:], proof.interactions[i][c].proofSet[2:])
		proofSet[0] = proof.interactions[i][1-c].proofSet[0]
		proofSet[1] = proof.interactions[i][1-c].proofSet[1]
		res = merkletree.VerifyProof(
			s.h,
			proof.interactions[i][1-c].merkleRoot,
			proofSet,
			uint64(si[i]+1-2*c),
			proof.interactions[i][1-c].numLeaves,
		)
		if !res {
			return ErrProximityTest
		}

		// correctness of the folding
		if i < len(proof.interactions)-1 {

			var fe, fo, l, r, fn fr.Element

			// even part
			l.SetBytes(proof.interactions[i][0].proofSet[0])
			r.SetBytes(proof.interactions[i][1].proofSet[0])
			fe.Add(&l, &r).Mul(&fe, &twoInv)

			// odd part
			m := convertSortedCanonical(si[i]-(si[i]%2), int(s.domains[i].Cardinality))
			bm := big.NewInt(int64(m))
			var ginv fr.Element
			ginv.Set(&s.domains[i].GeneratorInv).Exp(ginv, bm)
			fo.Sub(&l, &r).Mul(&fo, &twoInv).Mul(&fo, &ginv)
			fn.SetBytes(proof.interactions[i+1][si[i+1]%2].proofSet[0])

			// folding
			fo.Mul(&fo, &xi[i]).Add(&fo, &fe)
			if !fo.Equal(&fn) {
				return ErrProximityTest
			}
		}
	}

	// last transition
	var fe, fo, l, r, fn fr.Element

	// even part
	l.SetBytes(proof.interactions[s.nbSteps-1][0].proofSet[0])
	r.SetBytes(proof.interactions[s.nbSteps-1][1].proofSet[0])
	fe.Add(&l, &r).Mul(&fe, &twoInv)

	// odd part
	m := convertSortedCanonical(si[s.nbSteps-1]-(si[s.nbSteps-1]%2), int(s.domains[s.nbSteps-1].Cardinality))
	bm := big.NewInt(int64(m))
	var ginv fr.Element
	ginv.Set(&s.domains[s.nbSteps-1].GeneratorInv).Exp(ginv, bm)
	fo.Sub(&l, &r).Mul(&fo, &twoInv).Mul(&fo, &ginv)
	_si := convertSortedCanonical(si[s.nbSteps], rho)
	fn.Set(&proof.evaluation[_si])

	// folding
	fo.Mul(&fo, &xi[s.nbSteps-1]).Add(&fo, &fe)
	if !fo.Equal(&fn) {
		return ErrProximityTest
	}

	// Last step: check that the evatuations lie on a line
	dx := make([]fr.Element, rho-1)
	dy := make([]fr.Element, rho-1)
	var _g, g, one fr.Element
	g.Set(&s.domains[s.nbSteps-1].Generator).Square(&g)
	_g.Set(&g)
	one.SetOne()
	for i := 1; i < rho; i++ {
		dx[i-1].Sub(&g, &one)
		dy[i-1].Sub(&proof.evaluation[i], &proof.evaluation[i-1])
		g.Mul(&g, &_g)
	}
	dx = fr.BatchInvert(dx)
	dydx := make([]fr.Element, rho-1)
	for i := 1; i < rho; i++ {
		dydx[i-1].Mul(&dy[i-1], &dx[i-1])
	}

	for i := 1; i < rho-1; i++ {
		if !dydx[0].Equal(&dydx[i]) {
			return ErrProximityTest
		}
	}

	return nil
}

func eval(p []fr.Element, x fr.Element) fr.Element {
	var res fr.Element
	for i := len(p) - 1; i >= 0; i-- {
		res.Mul(&res, &x).Add(&res, &p[i])
	}
	return res
}
