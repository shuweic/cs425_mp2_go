package main

import (
	"errors"
	"fmt"
	"math/rand"
)

type MemberList struct {
	Members     []*Member
	size        int
	curPos      int
	shuffleList []int
}

type Member struct {
	TimeStamp uint64
	IP        uint32
	State     uint8
}

func NewMemberList(capacity int) *MemberList {
	ml := MemberList{}
	ml.Members = make([]*Member, capacity)
	Logger.Info("Member list created\n")
	return &ml
}

func (ml *MemberList) Size() int {
	return ml.size
}

// Return the member if exists, otherwise return error
func (ml *MemberList) Retrieve(ts uint64, ip uint32) (*Member, error) {
	idx := ml.Select(ts, ip)
	if idx > -1 {
		return ml.Members[idx], nil
	} else {
		return nil, errors.New(capitalizeString("Invalid retrieve ts and ip"))
	}
}

// Return the member if exists, otherwise return error
func (ml *MemberList) RetrieveByIdx(idx int) (*Member, error) {
	if idx < ml.size && idx > -1 {
		return ml.Members[idx], nil
	} else {
		return nil, errors.New(capitalizeString("Invalid retrieve index"))
	}
}

// If insert member exists, return err
func (ml *MemberList) Insert(m *Member) error {
	// Check whether insert member exists
	if ml.Select(m.TimeStamp, m.IP) != -1 {
		return errors.New("Member already exists")
	}

	// Resize when needed
	if ml.size == len(ml.Members) {
		ml.Resize(ml.size * 2)
	}
	// Insert new member
	ml.Members[ml.size] = m
	ml.size += 1
	// Log Insert
	Logger.Info("Insert member (%d, %d)\n", m.TimeStamp, m.IP)

	// Prolong the shuffle list
	ml.shuffleList = append(ml.shuffleList, len(ml.shuffleList))
	Logger.Info("Prolong the length of shuffleList to: %d\n", len(ml.shuffleList))
	return nil
}

// If delete member doesn't exist, return error
func (ml *MemberList) Delete(ts uint64, ip uint32) error {
	idx := ml.Select(ts, ip)
	if idx > -1 {
		// Shorten the shuffle list
		// Find the index of the maximum value in the shuffleList
		maxidx := 0
		for idx := 1; idx < len(ml.shuffleList); idx += 1 {
			if ml.shuffleList[idx] > ml.shuffleList[maxidx] {
				maxidx = idx
			}
		}
		// Delete this maximum value and shorten the shuffList
		ml.shuffleList[maxidx] = ml.shuffleList[len(ml.shuffleList)-1]
		ml.shuffleList = ml.shuffleList[:len(ml.shuffleList)-1]
		if len(ml.shuffleList) == 0 {
			ml.curPos = 0
		} else {
			ml.curPos %= len(ml.shuffleList)
		}
		Logger.Info("Shorten the length of shuffleList to: %d\n", len(ml.shuffleList))

		// Replace the delete member with the last member
		ml.Members[idx] = ml.Members[ml.size-1]
		ml.size -= 1
		Logger.Info("Delete member (%d, %d)\n", ts, ip)
		return nil
	} else {
		return errors.New(capitalizeString("Invalid delete"))
	}
}

// If update member doesn't exist, return error
func (ml *MemberList) Update(ts uint64, ip uint32, state uint8) error {
	idx := ml.Select(ts, ip)
	if idx > -1 {
		ml.Members[idx].State = state
		Logger.Info("Update member (%d, %d) to state: %b\n", ts, ip, state)
		return nil
	} else {
		return errors.New(capitalizeString("Invalid update"))
	}
}

func (ml *MemberList) Select(ts uint64, ip uint32) int {
	for idx := 0; idx < ml.size; idx += 1 {
		if (ml.Members[idx].TimeStamp == ts) && (ml.Members[idx].IP == ip) {
			// Search hit
			return idx
		}
	}
	// Search failed
	return -1
}

func (ml *MemberList) Resize(capacity int) {
	members := make([]*Member, capacity)
	// Copy arrays
	for idx := 0; idx < ml.size; idx += 1 {
		members[idx] = ml.Members[idx]
	}
	ml.Members = members
}

func (ml *MemberList) PrintMemberList() {
	fmt.Printf("------------------------------------------\n")
	fmt.Printf("Size: %d\n", ml.size)
	for idx := 0; idx < ml.size; idx += 1 {
		m := ml.Members[idx]
		fmt.Printf("idx: %d, TS: %d, IP: %s, ST: %b\n", idx,
			m.TimeStamp, int2ip(m.IP).String(), m.State)
	}
	fmt.Printf("------------------------------------------\n")
}

// Return an round-robin random member
func (ml *MemberList) Shuffle() *Member {
	// Shuffle the shuffleList when the curPos comes to the end
	if ml.curPos == (len(ml.shuffleList) - 1) {
		member := ml.Members[ml.shuffleList[ml.curPos]]
		ml.curPos = (ml.curPos + 1) % len(ml.shuffleList)
		// // Shuffle the shuffleList
		// rand.Shuffle(len(ml.shuffleList), func(i, j int) {
		// 	ml.shuffleList[i], ml.shuffleList[j] = ml.shuffleList[j], ml.shuffleList[i]
		// })
		// Shuffle without rand.Shuffle
		for i := range ml.shuffleList {
			j := rand.Intn(i + 1)
			ml.shuffleList[i], ml.shuffleList[j] = ml.shuffleList[j], ml.shuffleList[i]
		}
		return member
	} else {
		member := ml.Members[ml.shuffleList[ml.curPos]]
		ml.curPos = (ml.curPos + 1) % len(ml.shuffleList)
		return member
	}
}

// Return ture if IP exists in the list
func (ml *MemberList) ContainsIP(ip uint32) bool {
	for idx := 0; idx < ml.size; idx += 1 {
		if ml.Members[idx].IP == ip {
			return true
		}
	}
	return false
}

// // Test client
// func main() {
// 	ml := NewMemberList(1)

// 	m1 := Member{1, 1, 1}
// 	m2 := Member{2, 2, 2}
// 	m3 := Member{3, 3, 3}
// 	m4 := Member{4, 4, 4}
// 	m5 := Member{5, 5, 5}
// 	m6 := Member{6, 6, 6}
// 	m7 := Member{7, 7, 7}
// 	m8 := Member{8, 8, 8}
// 	m9 := Member{9, 9, 9}
// 	m10 := Member{10, 10, 10}

// 	// Test insert and delete
// 	ml.Insert(&m1)
// 	ml.Insert(&m2)
// 	ml.Insert(&m3)
// 	ml.Delete(3, 3)
// 	ml.Insert(&m4)
// 	ml.Insert(&m5)
// 	ml.Insert(&m6)

// 	// Test update
// 	x := ml.Retrieve(2, 2)
// 	fmt.Printf("origin state: %d\n", x.State)
// 	ml.Update(2, 2, 4)
// 	x = ml.Retrieve(2, 2)
// 	fmt.Printf("update state: %d\n", x.State)

// 	// Test shuffle for 4 rounds
// 	for i := 0; i < 4; i++ {
// 		for i := 0; i < len(ml.shuffleList); i++ {
// 			ml.Shuffle()
// 		}
// 	}

// 	// Test shuffle during insert and delete
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Insert(&m7)
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Insert(&m8)
// 	ml.Shuffle()
// 	ml.Delete(1,1)
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Insert(&m9)
// 	ml.Insert(&m10)
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// 	ml.Shuffle()
// }
