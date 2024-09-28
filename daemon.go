package main

import (
	"bytes"
	"encoding/binary"
	"fmt"

	// "log"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"
)

const (
	Ping               = 0x01
	Ack                = 0x01 << 1
	MemInitRequest     = 0x01 << 2
	MemInitReply       = 0x01 << 3
	MemUpdateSuspect   = 0x01 << 4
	MemUpdateResume    = 0x01 << 5
	MemUpdateLeave     = 0x01 << 6
	MemUpdateJoin      = 0x01 << 7
	StateAlive         = 0x01
	StateSuspect       = 0x01 << 1
	StateMonit         = 0x01 << 2
	StateIntro         = 0x01 << 3
	IntroducerIP       = "10.193.190.151"
	Port               = ":6666"
	InitTimeoutPeriod  = 2000 * time.Millisecond
	PingTimeoutPeriod  = 1000 * time.Millisecond
	PingSendingPeriod  = 250 * time.Millisecond
	SuspectPeriod      = 1000 * time.Millisecond
	PingIntroPeriod    = 5000 * time.Millisecond
	UpdateDeletePeriod = 15000 * time.Millisecond
	LeaveDelayPeriod   = 2000 * time.Millisecond
	TTL_               = 3
)

type Header struct {
	Type     uint8
	Seq      uint16
	Reserved uint8
}

type Update struct {
	UpdateID        uint64
	TTL             uint8
	UpdateType      uint8
	MemberTimeStamp uint64
	MemberIP        uint32
	MemberState     uint8
}

var init_timer *time.Timer
var PingAckTimeout map[uint16]*time.Timer
var FailureTimeout map[[2]uint64]*time.Timer
var CurrentMember *Member
var CurrentList *MemberList
var LocalIP string

var DuplicateUpdateCaches map[uint64]uint8
var TTLCaches *TtlCache
var Logger *ssmsLogger

var global_wg sync.WaitGroup

// mutex used for duplicate update caches write
var mutex sync.Mutex

// A trick to simply get local IP address
func getLocalIP() net.IP {
	dial, err := net.Dial("udp", "8.8.8.8:80")
	printError(err)
	localAddr := dial.LocalAddr().(*net.UDPAddr)
	dial.Close()

	return localAddr.IP
}

// Convert net.IP to uint32
func ip2int(ip net.IP) uint32 {
	if len(ip) == 16 {
		return binary.BigEndian.Uint32(ip[12:16])
	} else {
		return binary.BigEndian.Uint32(ip)
	}
}

// Convert uint32 to net.IP
func int2ip(binip uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, binip)
	return ip
}

// Helper function to print the err in process
func printError(err error) {
	if err != nil {
		Logger.Error(err.Error())
	}
}

// UDP send
func udpSend(addr string, packet []byte) {
	conn, err := net.Dial("udp", addr)
	printError(err)
	defer conn.Close()
	conn.Write(packet)
}

// UDP Daemon loop task
func udpDaemon() {
	serverAddr := Port
	udpAddr, err := net.ResolveUDPAddr("udp4", serverAddr)
	printError(err)
	// Listen the request
	listen, err := net.ListenUDP("udp", udpAddr)
	printError(err)

	// Use waitgroup
	var wg sync.WaitGroup
	wg.Add(1)

	userCmd := make(chan string)

	go readCommand(userCmd)

	global_wg.Add(1)
	go udpDaemonHandle(listen)
	go periodicPing()
	go periodicPingIntroducer()

	for {
		s := <-userCmd
		switch s {
		case "join":
			if CurrentList.Size() > 0 {
				fmt.Println("Already in the group")
				continue
			}
			global_wg.Done()

			if LocalIP == IntroducerIP {
				CurrentMember.State |= (StateIntro | StateMonit)
				CurrentList.Insert(CurrentMember)
			} else {
				// New member, send Init Request to the introducer
				initRequest(CurrentMember)
			}

		case "showlist":
			CurrentList.PrintMemberList()

		case "showid":
			fmt.Printf("Member (%d, %s)\n", CurrentMember.TimeStamp, LocalIP)

		case "leave":
			if CurrentList.Size() < 1 {
				fmt.Println("Haven't join the group")
				continue
			}
			global_wg.Add(1)
			initiateLeave()

		default:
			fmt.Println("Invalid Command, Please use correct one")
			fmt.Println("# join")
			fmt.Println("# showlist")
			fmt.Println("# showid")
			fmt.Println("# leave")
		}
	}

	wg.Wait()
}

// Concurrently read user input by chanel
func readCommand(input chan<- string) {
	for {
		var cmd string
		_, err := fmt.Scanln(&cmd)
		if err != nil {
			fmt.Println(err)
		}
		input <- cmd
	}
}

func initiateLeave() {
	uid := TTLCaches.RandGen.Uint64()
	update := Update{uid, TTL_, MemUpdateLeave, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
	// Clear current ttl cache and add delete update to the cache
	TTLCaches = NewTtlCache()
	TTLCaches.Set(&update)
	isUpdateDuplicate(uid)
	Logger.Info("Member (%d, %s) leaves", CurrentMember.TimeStamp, LocalIP)
	time.Sleep(LeaveDelayPeriod)
	initilize()
}

func periodicPingIntroducer() {
	for {
		global_wg.Wait() // Once leave, Do not execute this function
		// Periodiclly ping introducer when introducer is failed.
		// Piggyback it's self member info
		// Use for introducer revive
		if (CurrentList.Size() > 0) && (!CurrentList.ContainsIP(ip2int(net.ParseIP(IntroducerIP)))) && (LocalIP != IntroducerIP) {
			// Construct a join update
			uid := TTLCaches.RandGen.Uint64()
			update := Update{uid, TTL_, MemUpdateJoin, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
			isUpdateDuplicate(uid)
			// Construct a buffer to carry binary update struct
			var updateBuffer bytes.Buffer
			binary.Write(&updateBuffer, binary.BigEndian, &update)
			// Send piggyback Join Update
			Logger.Info("Introducer failed, try to ping introducer\n")
			pingWithPayload(&Member{0, ip2int(net.ParseIP(IntroducerIP)), 0}, updateBuffer.Bytes(), MemUpdateJoin)
		}

		// Ping introducer period
		time.Sleep(PingIntroPeriod)
	}
}

// Periodically ping a randomly selected target
func periodicPing() {
	for {
		// Shuffle membership list and get a member
		// Only executed when the membership list is not empty
		if CurrentList.Size() > 0 {
			member := CurrentList.Shuffle()
			// Do not pick itself as the ping target
			if (member.TimeStamp == CurrentMember.TimeStamp) && (member.IP == CurrentMember.IP) {
				time.Sleep(PingSendingPeriod)
				continue
			}
			Logger.Info("Member (%d, %d) is selected by shuffling\n", member.TimeStamp, member.IP)
			// Get update entry from TTL Cache
			update, flag, err := getUpdate()
			// if no update there, do pure ping
			if err != nil {
				ping(member)
			} else {
				// Send update as payload of ping
				pingWithPayload(member, update, flag)
			}
		}
		time.Sleep(PingSendingPeriod)
	}
}

func udpDaemonHandle(connect *net.UDPConn) {
	for {
		global_wg.Wait() // Once leave, stop receiving messages
		// Making a buffer to accept the grep command content from client
		buffer := make([]byte, 1024)
		n, addr, err := connect.ReadFromUDP(buffer)
		printError(err)

		// Seperate header and payload
		const HeaderLength = 4 // Header Length 4 bytes

		// Read header
		headerBinData := buffer[:HeaderLength]
		var header Header
		buf := bytes.NewReader(headerBinData)
		err = binary.Read(buf, binary.BigEndian, &header)
		printError(err)

		// Read payload
		payload := buffer[HeaderLength:n]

		// Resume detection

		if header.Type&Ping != 0 {

			reserved := uint8(0x00)
			// Check whether this ping's source IP is within the memberlist
			// IF not, set reserved 0xff, ask for sender's join update
			// init request will not participate this procedure
			// Because every new join member is unknown to the introducer
			if (!CurrentList.ContainsIP(ip2int(addr.IP))) && (header.Type&MemInitRequest == 0) {
				reserved = 0xff
				Logger.Info("Receive ping from unknown member, set reserved field 0xff")
			}

			// Check whether this ping carries Init Request
			if header.Type&MemInitRequest != 0 {
				// Handle Init Request
				Logger.Info("Receive Init Request from %s: with seq %d\n", addr.IP.String(), header.Seq)
				initReply(addr.IP.String(), header.Seq, payload)

			} else if header.Type&MemUpdateSuspect != 0 {
				Logger.Info("Handle suspect update sent from %s\n", addr.IP.String())
				handleSuspect(payload)
				// Get update entry from TTL Cache
				update, flag, err := getUpdate()
				// if no update there, do pure ping
				if err != nil {
					ack(addr.IP.String(), header.Seq, reserved)
				} else {
					// Send update as payload of ping
					ackWithPayload(addr.IP.String(), header.Seq, update, flag, reserved)
				}

			} else if header.Type&MemUpdateResume != 0 {
				Logger.Info("Handle resume update sent from %s\n", addr.IP.String())
				handleResume(payload)
				// Get update entry from TTL Cache
				update, flag, err := getUpdate()
				// if no update there, do pure ping
				if err != nil {
					ack(addr.IP.String(), header.Seq, reserved)
				} else {
					// Send update as payload of ping
					ackWithPayload(addr.IP.String(), header.Seq, update, flag, reserved)
				}

			} else if header.Type&MemUpdateLeave != 0 {
				Logger.Info("Handle leave update sent from %s\n", addr.IP.String())
				handleLeave(payload)
				// Get update entry from TTL Cache
				update, flag, err := getUpdate()
				// if no update there, do pure ping
				if err != nil {
					ack(addr.IP.String(), header.Seq, reserved)
				} else {
					// Send update as payload of ping
					ackWithPayload(addr.IP.String(), header.Seq, update, flag, reserved)
				}

			} else if header.Type&MemUpdateJoin != 0 {
				Logger.Info("Handle join update sent from %s\n", addr.IP.String())
				handleJoin(payload)
				// Get update entry from TTL Cache
				update, flag, err := getUpdate()
				// if no update there, do pure ping
				if err != nil {
					ack(addr.IP.String(), header.Seq, reserved)
				} else {
					// Send update as payload of ping
					ackWithPayload(addr.IP.String(), header.Seq, update, flag, reserved)
				}

			} else {
				// Ping with no payload,
				// No handling payload needed
				// Check whether update sending needed
				// If no, simply reply with ack
				ack(addr.IP.String(), header.Seq, reserved)
			}

		} else if header.Type&Ack != 0 {

			// Receive Ack, stop ping timer
			timer, ok := PingAckTimeout[header.Seq-1]
			if ok {
				timer.Stop()
				Logger.Info("Receive ACK from [%s] with seq %d\n", addr.IP.String(), header.Seq)
				delete(PingAckTimeout, header.Seq-1)
			}

			// Check header's reserved field
			// If reserved field is 0xff, means this handler is missing in someone else's memberlist,
			// Hence disseminate join update
			if header.Reserved == 0xff {
				uid := TTLCaches.RandGen.Uint64()
				update := Update{uid, TTL_, MemUpdateJoin, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
				TTLCaches.Set(&update)
				isUpdateDuplicate(uid)
				Logger.Info("Receive header with reserved 0xff, disseminate join update")
			}

			// Read payload
			payload := buffer[HeaderLength:n]

			if header.Type&MemInitReply != 0 {
				// Ack carries Init Reply, stop init timer
				stop := init_timer.Stop()
				if stop {
					Logger.Info("Receive Init Reply from [%s] with %d\n", addr.IP.String(), header.Seq)
				}
				handleInitReply(payload)

			} else if header.Type&MemUpdateSuspect != 0 {
				Logger.Info("Handle suspect update sent from %s\n", addr.IP.String())
				handleSuspect(payload)

			} else if header.Type&MemUpdateResume != 0 {
				Logger.Info("Handle resume update sent from %s\n", addr.IP.String())
				handleResume(payload)

			} else if header.Type&MemUpdateLeave != 0 {
				Logger.Info("Handle leave update sent from %s\n", addr.IP.String())
				handleLeave(payload)

			} else if header.Type&MemUpdateJoin != 0 {
				Logger.Info("Handle join update sent from %s\n", addr.IP.String())
				handleJoin(payload)

			} else {
				Logger.Info("Receive pure ack sent from %s\n", addr.IP.String())
			}
		}
	}
}

// Check whether the update is duplicated
// If duplicated, return false, else, return true and start a timer
func isUpdateDuplicate(id uint64) bool {
	mutex.Lock()
	_, ok := DuplicateUpdateCaches[id]
	mutex.Unlock()
	if ok {
		Logger.Info("Receive duplicated update %d\n", id)
		return true
	} else {
		mutex.Lock()
		DuplicateUpdateCaches[id] = 1 // add to cache
		mutex.Unlock()
		Logger.Info("Add update %d to duplicated cache table \n", id)
		recent_update_timer := time.NewTimer(UpdateDeletePeriod) // set a delete timer
		go func() {
			<-recent_update_timer.C
			mutex.Lock()
			_, ok := DuplicateUpdateCaches[id]
			mutex.Unlock()
			if ok {
				mutex.Lock()
				delete(DuplicateUpdateCaches, id) // delete from cache
				mutex.Unlock()
				Logger.Info("Delete update %d from duplicated cache table \n", id)
			}
		}()
		return false
	}
}

func getUpdate() ([]byte, uint8, error) {
	var binBuffer bytes.Buffer

	update, err := TTLCaches.Get()
	if err != nil {
		return binBuffer.Bytes(), 0, err
	}

	binary.Write(&binBuffer, binary.BigEndian, update)
	return binBuffer.Bytes(), update.UpdateType, nil
}

func handleSuspect(payload []byte) {
	buf := bytes.NewReader(payload)
	var update Update
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	// Retrieve update ID
	updateID := update.UpdateID
	if !isUpdateDuplicate(updateID) {
		// If find someone sends suspect update which
		// suspect self, tell them I am alvie
		if CurrentMember.TimeStamp == update.MemberTimeStamp && CurrentMember.IP == update.MemberIP {
			addUpdate2Cache(CurrentMember, MemUpdateResume)
			return
		}
		// Receive new update, handle it
		CurrentList.Update(update.MemberTimeStamp, update.MemberIP, update.MemberState)
		TTLCaches.Set(&update)
		failure_timer := time.NewTimer(SuspectPeriod)
		FailureTimeout[[2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)}] = failure_timer
		go func() {
			<-failure_timer.C
			Logger.Info("[Failure Detected](%s, %d) Failed, detected by others\n", int2ip(update.MemberIP).String(), update.MemberTimeStamp)
			err := CurrentList.Delete(update.MemberTimeStamp, update.MemberIP)
			printError(err)
			delete(FailureTimeout, [2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)})
		}()

	}
}

func handleResume(payload []byte) {
	buf := bytes.NewReader(payload)
	var update Update
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	// Retrieve update ID
	updateID := update.UpdateID
	if !isUpdateDuplicate(updateID) {
		// Receive new update, handle it
		timer, ok := FailureTimeout[[2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)}]
		if ok {
			timer.Stop()
			delete(FailureTimeout, [2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)})
		}
		err := CurrentList.Update(update.MemberTimeStamp, update.MemberIP, update.MemberState)
		// If the resume target is not in the list, insert it to the list
		if err != nil {
			CurrentList.Insert(&Member{update.MemberTimeStamp, update.MemberIP, update.MemberState})
		}
		TTLCaches.Set(&update)
	}
}

func handleLeave(payload []byte) {
	buf := bytes.NewReader(payload)
	var update Update
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	// Retrieve update ID
	updateID := update.UpdateID
	if !isUpdateDuplicate(updateID) {
		// Receive new update, handle it
		CurrentList.Delete(update.MemberTimeStamp, update.MemberIP)
		TTLCaches.Set(&update)
	}
}

func handleJoin(payload []byte) {
	buf := bytes.NewReader(payload)
	var update Update
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	// Retrieve update ID
	updateID := update.UpdateID
	if !isUpdateDuplicate(updateID) {
		// Receive new update, handle it
		CurrentList.Insert(&Member{update.MemberTimeStamp, update.MemberIP,
			update.MemberState})
		TTLCaches.Set(&update)
		// Introducer diseeminate its info when receives join
		if LocalIP == IntroducerIP {
			uid := TTLCaches.RandGen.Uint64()
			reply_update := Update{uid, TTL_, MemUpdateJoin, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
			TTLCaches.Set(&reply_update)
			isUpdateDuplicate(uid)
			Logger.Info("Introducer set its info update to the cache\n")
		}
	}
}

// Generate a new update and set it in TTL Cache
func addUpdate2Cache(member *Member, updateType uint8) {
	uid := TTLCaches.RandGen.Uint64()
	update := Update{uid, TTL_, updateType, member.TimeStamp, member.IP, member.State}
	TTLCaches.Set(&update)
	// This daemon is the update producer, add this update to the update duplicate cache
	isUpdateDuplicate(uid)
}

// Handle the full membership list(InitReply) received from introducer
func handleInitReply(payload []byte) {
	num := len(payload) / 13 // 13 bytes per member
	buf := bytes.NewReader(payload)
	for idx := 0; idx < num; idx++ {
		var member Member
		err := binary.Read(buf, binary.BigEndian, &member)
		printError(err)
		// Insert existing member to the new member's list
		CurrentList.Insert(&member)
	}
}

// Introducer replies new node join init request and
// send the new node join updates to others in membership
func initReply(addr string, seq uint16, payload []byte) {
	// Read and insert new member to the memberlist
	var member Member
	buf := bytes.NewReader(payload)
	err := binary.Read(buf, binary.BigEndian, &member)
	printError(err)
	// Update state of the new member
	// ...
	CurrentList.Insert(&member)
	addUpdate2Cache(&member, MemUpdateJoin)

	// Put the entire memberlist to the Init Reply's payload
	var memBuffer bytes.Buffer // Temp buf to store member's binary value
	var binBuffer bytes.Buffer

	for i := 0; i < CurrentList.Size(); i += 1 {
		member_, _ := CurrentList.RetrieveByIdx(i)

		binary.Write(&memBuffer, binary.BigEndian, member_)
		binBuffer.Write(memBuffer.Bytes())
		memBuffer.Reset() // Clear buffer
	}

	// Send pigggback Init Reply
	ackWithPayload(addr, seq, binBuffer.Bytes(), MemInitReply, 0x00)
}

func initRequest(member *Member) {
	// Construct Init Request payload
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, member)

	// Send piggyback Init Request
	pingWithPayload(&Member{0, ip2int(net.ParseIP(IntroducerIP)), 0}, binBuffer.Bytes(), MemInitRequest)

	// Start Init timer, if expires, exit process
	init_timer = time.NewTimer(InitTimeoutPeriod)
	go func() {
		<-init_timer.C
		Logger.Info("Init %s timeout, process exit\n", IntroducerIP)
		os.Exit(1)
	}()
}

func ackWithPayload(addr string, seq uint16, payload []byte, flag uint8, reserved uint8) {
	packet := Header{Ack | flag, seq + 1, reserved}
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, packet)

	if payload != nil {
		binBuffer.Write(payload) // Append payload
		udpSend(addr+Port, binBuffer.Bytes())
	} else {
		udpSend(addr+Port, binBuffer.Bytes())
	}
}

func ack(addr string, seq uint16, reserved uint8) {
	ackWithPayload(addr, seq, nil, 0x00, reserved)
}

func pingWithPayload(member *Member, payload []byte, flag uint8) {
	// Source for genearting random number
	randSource := rand.NewSource(time.Now().UnixNano())
	randGen := rand.New(randSource)
	seq := randGen.Intn(0x01<<15 - 2)
	addr := int2ip(member.IP).String() + Port

	packet := Header{Ping | flag, uint16(seq), 0}
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, packet)

	if payload != nil {
		binBuffer.Write(payload) // Append payload
		udpSend(addr, binBuffer.Bytes())
	} else {
		udpSend(addr, binBuffer.Bytes())
	}
	Logger.Info("Ping (%s, %d)\n", addr, seq)

	timer := time.NewTimer(PingTimeoutPeriod)
	PingAckTimeout[uint16(seq)] = timer
	go func() {
		<-timer.C
		Logger.Info("Ping (%s, %d) timeout\n", addr, seq)
		err := CurrentList.Update(member.TimeStamp, member.IP, StateSuspect)
		if err == nil {
			addUpdate2Cache(member, MemUpdateSuspect)
		}
		delete(PingAckTimeout, uint16(seq))
		// Handle local suspect timeout
		failure_timer := time.NewTimer(SuspectPeriod)
		FailureTimeout[[2]uint64{member.TimeStamp, uint64(member.IP)}] = failure_timer
		go func() {
			<-failure_timer.C
			Logger.Info("[Failure Detected](%s, %d) Failed, detected by self\n", int2ip(member.IP).String(), member.TimeStamp)
			err := CurrentList.Delete(member.TimeStamp, member.IP)
			printError(err)
			delete(FailureTimeout, [2]uint64{member.TimeStamp, uint64(member.IP)})
		}()
	}()
}

func ping(member *Member) {
	pingWithPayload(member, nil, 0x00)
}

// Start the membership service and join in the group
func initilize() bool {
	// Create self entry
	LocalIP = getLocalIP().String()
	Logger = NewSsmsLogger(LocalIP)
	timestamp := time.Now().UnixNano()
	state := StateAlive
	CurrentMember = &Member{uint64(timestamp), ip2int(getLocalIP()), uint8(state)}

	// Create member list
	CurrentList = NewMemberList(20)

	// Make necessary tables
	PingAckTimeout = make(map[uint16]*time.Timer)
	FailureTimeout = make(map[[2]uint64]*time.Timer)
	DuplicateUpdateCaches = make(map[uint64]uint8)
	TTLCaches = NewTtlCache()

	return true
}

// Main func
func main() {

	// Init
	if initilize() == true {
		fmt.Printf("[INFO]: Start service\n")
	}
	// Start daemon
	udpDaemon()
}
