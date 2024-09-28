package main

import (
	"errors"
	//"fmt"
	"math/rand"
	"strings"
	"time"
)

// TTL Map, type as map[uint64]*Update
type TtlCache struct {
	TtlList []*Update
	Pointer int
	RandGen *rand.Rand
}

// Return a new TTL Map
func NewTtlCache() *TtlCache {
	// Source for genearting random number
	randSource := rand.NewSource(time.Now().UnixNano())
	randGen := rand.New(randSource)
	ttllist := make([]*Update, 0)

	Logger.Debug("TTL cache created\n")
	ttlcache := TtlCache{ttllist, 0, randGen}
	return &ttlcache
}

// Set the update packet in TTL Cache
func (tc *TtlCache) Set(val *Update) {
	if val.TTL < 1 {
		Logger.Debug("TTL cache cannot set for ttl=0 %d\n", val.UpdateID)
		return
	}
	tc.TtlList = append(tc.TtlList, val)
	Logger.Debug("TTL cache add a new update ID: %d, TTL: %d\n", val.UpdateID, val.TTL)
}

func capitalizeString(str string) string {
	if len(str) == 0 {
		return str
	}

	return strings.ToUpper(string(str[0])) + str[1:]
}

// Get one entry each time in TTL Cache
func (tc *TtlCache) Get() (*Update, error) {
	if len(tc.TtlList) == 0 {
		Logger.Debug(capitalizeString("TTL cache empty\n"))
		return nil, errors.New(capitalizeString("Empty TTL List, cannot Get()"))
	}
	cur := tc.TtlList[tc.Pointer]
	// Copy current update
	update := Update{cur.UpdateID, cur.TTL, cur.UpdateType, cur.MemberTimeStamp, cur.MemberIP, cur.MemberState}
	cur.TTL -= 1
	if cur.TTL < 1 {
		Logger.Debug("TTL cache expired %d\n", cur.UpdateID)
		// Delete this entry
		copy(tc.TtlList[tc.Pointer:], tc.TtlList[tc.Pointer+1:])
		tc.TtlList[len(tc.TtlList)-1] = nil
		tc.TtlList = tc.TtlList[:len(tc.TtlList)-1]
	}
	if len(tc.TtlList) != 0 {
		tc.Pointer = (tc.Pointer + 1) % len(tc.TtlList)
	} else {
		tc.Pointer = 0
	}
	return &update, nil
}

/*func main() {*/
//tc := NewTtlCache()
//u1 := Update{0, 3}
//tc.Set(&u1)
//tc.Set(&Update{0, 3})
//fmt.Println(len(tc.TtlList))
//u, err := tc.Get()
//fmt.Println(len(tc.TtlList), u.UpdateID)
//u, err = tc.Get()
//fmt.Println(len(tc.TtlList), u.UpdateID)
//u, err = tc.Get()
//fmt.Println(len(tc.TtlList), u.UpdateID)
//if err != nil {
//fmt.Println("ERR ", err)
//}
//u, err = tc.Get()
//fmt.Println(len(tc.TtlList), u.UpdateID)
//if err != nil {
//fmt.Println("ERR ", err)
//}
/*}*/
