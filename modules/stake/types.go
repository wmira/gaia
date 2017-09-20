package stake

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/cosmos/cosmos-sdk"
	abci "github.com/tendermint/abci/types"
)

// DelegateeBond defines the total amount of bond tokens and their exchange rate to
// coins, associated with a single validator. Accumulation of interest is modelled
// as an in increase in the exchange rate, and slashing as a decrease.
// When coins are delegated to this validator, the delegatee is credited
// with a DelegatorBond whose number of bond tokens is based on the amount of coins
// delegated divided by the current exchange rate. Voting power can be calculated as
// total bonds multiplied by exchange rate.
type DelegateeBond struct {
	Delegatee       sdk.Actor
	Commission      Decimal
	ExchangeRate    Decimal   // Exchange rate for this validator's bond tokens (in millionths of coins)
	TotalBondTokens Decimal   // Total number of bond tokens for the delegatee
	Account         sdk.Actor // Account where the bonded coins are held. Controlled by the app
	VotingPower     Decimal   // Last calculated voting power based on bond value
}

// Validator - Get the validator from a bond value
func (b DelegateeBond) Validator() *abci.Validator {
	return &abci.Validator{
		PubKey: b.Delegatee.Address,
		Power:  uint64(b.VotingPower.IntPart()), //TODO could be a problem sending in truncated IntPart here
	}
}

//--------------------------------------------------------------------------------

// DelegateeBonds - the set of all DelegateeBonds
type DelegateeBonds []DelegateeBond

var _ sort.Interface = DelegateeBonds{} //enforce the sort interface at compile time

// nolint - sort interface functions
func (b DelegateeBonds) Len() int      { return len(b) }
func (b DelegateeBonds) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b DelegateeBonds) Less(i, j int) bool {
	vp1, vp2 := b[i].VotingPower, b[j].VotingPower
	d1, d2 := b[i].Delegatee, b[j].Delegatee
	switch {
	case vp1 != vp2:
		return vp1.GT(vp2)
	case d1.ChainID < d2.ChainID:
		return true
	case d1.ChainID > d2.ChainID:
		return false
	case d1.App < d2.App:
		return true
	case d1.App > d2.App:
		return false
	default:
		return bytes.Compare(d1.Address, d2.Address) == -1
	}
}

// Sort - Sort the array of bonded values
func (b DelegateeBonds) Sort() {
	sort.Sort(b)
}

// UpdateVotingPower - voting power based on the bond value
func (b DelegateeBonds) UpdateVotingPower() (totalPower Decimal) {

	// First update the voting power for all delegatees be sure to give no
	// power to validators without the minimum atoms required to be a validator
	for _, bv := range b {
		vp := bv.TotalBondTokens.Mul(bv.ExchangeRate)
		if vp.LT(minValBond) {
			bv.VotingPower = Zero
		} else {
			bv.VotingPower = vp
		}
	}

	// Now sort and truncate the power
	b.Sort()
	for i, bv := range b {
		if i <= maxVal {
			totalPower = totalPower.Add(bv.VotingPower)
		} else {
			bv.VotingPower = Zero
		}
	}
	return
}

// GetValidators - get the most recent updated validator set from the
// DelegateeBonds. These bonds are already sorted by VotingPower from
// the UpdateVotingPower function which is the only function which
// is to modify the VotingPower
func (b DelegateeBonds) GetValidators(maxVal int) []*abci.Validator {
	validators := make([]*abci.Validator, 0, maxVal)
	for _, bv := range b {
		if bv.VotingPower.Equal(Zero) { //exit as soon as the first Voting power set to zero is found
			break
		}
		validators = append(validators, bv.Validator())
	}
	return validators
}

// ValidatorsDiff - get the difference in the validator set from the input validator set
func (b DelegateeBonds) ValidatorsDiff(previous []*abci.Validator, maxVal int) (diff []*abci.Validator) {

	//Get the new validator set
	new := b.GetValidators(maxVal)

	//calculate any differences from the previous to the new validator set
	diff = make([]*abci.Validator, 0, maxVal)
	for _, prev := range previous {

		//test for a difference between the validator power of validator
		currentPower := getValidatorPower(new, prev.PubKey)
		if currentPower != prev.Power {
			diff = append(diff, &abci.Validator{prev.PubKey, currentPower})
		}
	}
	return
}

// TODO remove this code, could be made much more efficient
// getValidatorPower - return the validator power with the matching pubKey from the validator list
func getValidatorPower(set []*abci.Validator, pubKey []byte) uint64 {
	for _, validator := range set {
		if bytes.Equal(validator.PubKey, pubKey) {
			return validator.Power
		}
	}
	return 0 // no power if not found
}

// Get - get a DelegateeBond for a specific validator from the DelegateeBonds
func (b DelegateeBonds) Get(delegatee sdk.Actor) (int, *DelegateeBond) {
	for i, bv := range b {
		if bv.Delegatee.Equals(delegatee) {
			return i, &bv
		}
	}
	return 0, nil
}

// Remove - remove delegatee from the delegatee list
func (b DelegateeBonds) Remove(i int) (DelegateeBonds, error) {
	switch {
	case i < 0:
		return b, fmt.Errorf("Cannot remove a negative element")
	case i >= len(b):
		return b, fmt.Errorf("Element is out of upper bound")
	default:
		return append(b[:i], b[i+1:]...), nil
	}
}

////////////////////////////////////////////////////////////////////////////////

// DelegatorBond represents some bond tokens held by an account.
// It is owned by one delegator, and is associated with the voting power of one delegatee.
type DelegatorBond struct {
	Delegatee  sdk.Actor
	BondTokens Decimal // amount of bond tokens
}

// DelegatorBonds - all delegator bonds existing with multiple delegatees
type DelegatorBonds []DelegatorBond

// Get - get a DelegateeBond for a specific validator from the DelegateeBonds
func (b DelegatorBonds) Get(delegatee sdk.Actor) (int, *DelegatorBond) {
	for i, bv := range b {
		if bytes.Equal(bv.Delegatee.Address, delegatee.Address) &&
			bv.Delegatee.ChainID == delegatee.ChainID &&
			bv.Delegatee.App == delegatee.App {
			return i, &bv
		}
	}
	return 0, nil
}

// Remove - remove delegatee from the delegatee list
func (b DelegatorBonds) Remove(i int) (DelegatorBonds, error) {
	switch {
	case i < 0:
		return b, fmt.Errorf("Cannot remove a negative element")
	case i >= len(b):
		return b, fmt.Errorf("Element is out of upper bound")
	default:
		return append(b[:i], b[i+1:]...), nil
	}
}

////////////////////////////////////////////////////////////////////////////////

// QueueElem - queue element, the basis of a queue interaction with a delegatee/validator
type QueueElem struct {
	Delegatee    sdk.Actor
	HeightAtInit uint64 // when the queue was initiated
}

// QueueElemUnbond - the unbonding queue element
type QueueElemUnbond struct {
	QueueElem
	Account    sdk.Actor // account to pay out to
	BondTokens Decimal   // amount of bond tokens which are unbonding
}

// QueueElemModComm - the commission queue element
type QueueElemModComm struct {
	QueueElem
	CommChange Decimal // Proposed change in commission
}
