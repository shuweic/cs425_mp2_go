package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"
)

// Constants for message types
const (
	MessageTypePing           = 1
	MessageTypeAck            = 2
	MessageTypeMemInitRequest = 3
	MessageTypeMemInitReply   = 4
	MessageTypeUpdate         = 5
	MessageTypeFlagUpdate     = 6
)

// Constants for update types
const (
	UpdateTypeSuspect = 1
	UpdateTypeResume  = 2
	UpdateTypeLeave   = 3
	UpdateTypeJoin    = 4
)

const (
	StateAlive   = 0x01
	StateSuspect = 0x02
	StateMonit   = 0x04
	StateIntro   = 0x08

	IntroducerIP       = "172.22.95.32"
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
var suspectOn bool

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

		case "list_mem":
			CurrentList.PrintMemberList()

		case "list_self":
			fmt.Printf("Member (%d, %s)\n", CurrentMember.TimeStamp, LocalIP)

		case "leave":
			if CurrentList.Size() < 1 {
				fmt.Println("Haven't join the group")
				continue
			}
			global_wg.Add(1)
			initiateLeave()
		case "enable_sus":
			suspectOn = true
			fmt.Println("Suspicion mechanism enabled.")
			broadcastFlagChange(suspectOn)

		case "disable_sus":
			suspectOn = false
			fmt.Println("Suspicion mechanism disabled.")
			broadcastFlagChange(suspectOn)

		case "status_sus":
			if suspectOn {
				fmt.Println("Suspicion mechanism is enabled.")
			} else {
				fmt.Println("Suspicion mechanism is disabled.")
			}

		default:
			fmt.Println("Invalid Command, Please use correct one")
			fmt.Println("# join")
			fmt.Println("# list_mem")
			fmt.Println("# list_self")
			fmt.Println("# leave")
			fmt.Println("# enable_sus")
			fmt.Println("# disable_sus")
			fmt.Println("# status_sus")
		}
	}

	wg.Wait()
}

// Concurrently read user input by channel
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
	update := Update{uid, TTL_, UpdateTypeLeave, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
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
		// Periodically ping introducer when introducer is failed.
		// Piggyback its own member info
		// Use for introducer revive
		if (CurrentList.Size() > 0) && (!CurrentList.ContainsIP(ip2int(net.ParseIP(IntroducerIP)))) && (LocalIP != IntroducerIP) {
			// Construct a join update
			uid := TTLCaches.RandGen.Uint64()
			update := Update{uid, TTL_, UpdateTypeJoin, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
			isUpdateDuplicate(uid)
			// Construct a buffer to carry binary update struct
			var updateBuffer bytes.Buffer
			binary.Write(&updateBuffer, binary.BigEndian, &update)
			// Send piggyback Join Update
			Logger.Info("Introducer failed, try to ping introducer\n")
			pingWithPayload(&Member{0, ip2int(net.ParseIP(IntroducerIP)), 0}, updateBuffer.Bytes())
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
			updatePayload, err := getUpdatePayload()
			// if no update there, do pure ping
			if err != nil {
				ping(member)
			} else {
				// Send update as payload of ping
				pingWithPayload(member, updatePayload)
			}
		}
		time.Sleep(PingSendingPeriod)
	}
}

func udpDaemonHandle(connect *net.UDPConn) {
	for {
		global_wg.Wait() // Once leave, stop receiving messages
		// Making a buffer to accept the command content from client
		buffer := make([]byte, 1024)
		n, addr, err := connect.ReadFromUDP(buffer)
		printError(err)

		// Separate header and payload
		const HeaderLength = 4 // Header Length 4 bytes

		// Read header
		headerBinData := buffer[:HeaderLength]
		var header Header
		buf := bytes.NewReader(headerBinData)
		err = binary.Read(buf, binary.BigEndian, &header)
		printError(err)

		// Read payload
		payload := buffer[HeaderLength:n]

		// Handle messages based on their type
		switch header.Type {
		case MessageTypePing:
			handlePing(header, addr, payload)
		case MessageTypeAck:
			handleAck(header, addr, payload)
		case MessageTypeMemInitRequest:
			handleMemInitRequest(header, addr, payload)
		case MessageTypeMemInitReply:
			handleMemInitReply(header, addr, payload)
		case MessageTypeUpdate:
			handleUpdate(header, addr, payload)
		case MessageTypeFlagUpdate:
			handleFlagUpdate(payload)
		default:
			Logger.Error("Unknown message type: %d", header.Type)
		}
	}
}

// Check whether the update is duplicated
// If duplicated, return true, else, return false and start a timer
func isUpdateDuplicate(id uint64) bool {
	mutex.Lock()
	_, ok := DuplicateUpdateCaches[id]
	if ok {
		mutex.Unlock()
		Logger.Info("Receive duplicated update %d\n", id)
		return true
	} else {
		DuplicateUpdateCaches[id] = 1 // add to cache
		mutex.Unlock()
		Logger.Info("Add update %d to duplicated cache table \n", id)
		recent_update_timer := time.NewTimer(UpdateDeletePeriod) // set a delete timer
		go func() {
			<-recent_update_timer.C
			mutex.Lock()
			_, ok := DuplicateUpdateCaches[id]
			if ok {
				delete(DuplicateUpdateCaches, id) // delete from cache
				Logger.Info("Delete update %d from duplicated cache table \n", id)
			}
			mutex.Unlock()
		}()
		return false
	}
}

func getUpdatePayload() ([]byte, error) {
	var binBuffer bytes.Buffer

	update, err := TTLCaches.Get()
	if err != nil {
		return nil, err
	}

	binary.Write(&binBuffer, binary.BigEndian, update)
	return binBuffer.Bytes(), nil
}

func handlePing(header Header, addr *net.UDPAddr, payload []byte) {
	reserved := uint8(0x00)
	senderIP := ip2int(addr.IP)

	// Check whether this ping's source IP is within the member list
	if !CurrentList.ContainsIP(senderIP) {
		reserved = 0xff
		Logger.Info("Received ping from unknown member, set reserved field 0xff")
	}

	if len(payload) > 0 {
		// Handle the payload as an update
		handleUpdate(header, addr, payload)
	}

	ack(addr.IP.String(), header.Seq, reserved)
}

func handleAck(header Header, addr *net.UDPAddr, payload []byte) {
	timer, ok := PingAckTimeout[header.Seq]
	if ok && timer != nil {
		timer.Stop()
		Logger.Info("Receive ACK from [%s] with seq %d\n", addr.IP.String(), header.Seq)
		delete(PingAckTimeout, header.Seq)
	} else {
		Logger.Debug("Attempted to stop a non-existent or nil timer for seq %d\n", header.Seq)
	}

	// Check header's reserved field
	if header.Reserved == 0xff {
		// Disseminate join update
		uid := TTLCaches.RandGen.Uint64()
		update := Update{uid, TTL_, UpdateTypeJoin, CurrentMember.TimeStamp, CurrentMember.IP, CurrentMember.State}
		TTLCaches.Set(&update)
		isUpdateDuplicate(uid)
		Logger.Info("Received ACK with reserved 0xff, disseminate join update")
	}

	if len(payload) > 0 {
		// Handle the payload as an update
		handleUpdate(header, addr, payload)
	}
}

func handleMemInitRequest(header Header, addr *net.UDPAddr, payload []byte) {
	Logger.Info("Receive Init Request from %s: with seq %d\n", addr.IP.String(), header.Seq)
	initReply(addr.IP.String(), header.Seq, payload)
}

func handleMemInitReply(header Header, addr *net.UDPAddr, payload []byte) {
	if stop := init_timer.Stop(); stop {
		Logger.Info("Receive Init Reply from [%s] with %d\n", addr.IP.String(), header.Seq)
	}
	handleInitReply(payload)
}

func handleUpdate(header Header, addr *net.UDPAddr, payload []byte) {
	var update Update
	buf := bytes.NewReader(payload)
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	switch update.UpdateType {
	case UpdateTypeSuspect:
		handleSuspectUpdate(&update)
	case UpdateTypeResume:
		handleResumeUpdate(&update)
	case UpdateTypeLeave:
		handleLeaveUpdate(&update)
	case UpdateTypeJoin:
		handleJoinUpdate(&update)
	default:
		Logger.Error("Unknown update type: %d", update.UpdateType)
	}
}

func handleFlagUpdate(payload []byte) {
	var update Update
	buf := bytes.NewReader(payload)
	err := binary.Read(buf, binary.BigEndian, &update)
	printError(err)

	// Update suspicion mechanism status
	if update.MemberState == 1 {
		suspectOn = true
		fmt.Println("Suspicion mechanism enabled (received from another node).")
	} else {
		suspectOn = false
		fmt.Println("Suspicion mechanism disabled (received from another node).")
	}
}

func handleSuspectUpdate(update *Update) {
	if isUpdateDuplicate(update.UpdateID) {
		return
	}
	// If the suspect update is about ourselves
	if CurrentMember.TimeStamp == update.MemberTimeStamp && CurrentMember.IP == update.MemberIP {
		addUpdate2Cache(CurrentMember, UpdateTypeResume)
		return
	}
	// Update member state to suspect
	CurrentList.Update(update.MemberTimeStamp, update.MemberIP, StateSuspect)
	TTLCaches.Set(update)
	// Start failure timer
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

func handleResumeUpdate(update *Update) {
	if isUpdateDuplicate(update.UpdateID) {
		return
	}
	// Cancel failure timer if exists
	timer, ok := FailureTimeout[[2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)}]
	if ok {
		timer.Stop()
		delete(FailureTimeout, [2]uint64{update.MemberTimeStamp, uint64(update.MemberIP)})
	}
	// Update member state to alive
	err := CurrentList.Update(update.MemberTimeStamp, update.MemberIP, update.MemberState)
	if err != nil {
		CurrentList.Insert(&Member{update.MemberTimeStamp, update.MemberIP, update.MemberState})
	}
	TTLCaches.Set(update)
}

func handleLeaveUpdate(update *Update) {
	if isUpdateDuplicate(update.UpdateID) {
		return
	}
	CurrentList.Delete(update.MemberTimeStamp, update.MemberIP)
	TTLCaches.Set(update)
}

func handleJoinUpdate(update *Update) {
	if isUpdateDuplicate(update.UpdateID) {
		return
	}
	CurrentList.Insert(&Member{update.MemberTimeStamp, update.MemberIP, update.MemberState})
	TTLCaches.Set(update)
	// If this node is the introducer, disseminate its info
	if LocalIP == IntroducerIP {
		uid := TTLCaches.RandGen.Uint64()
		reply_update := Update{
			UpdateID:        uid,
			TTL:             TTL_,
			UpdateType:      UpdateTypeJoin,
			MemberTimeStamp: CurrentMember.TimeStamp,
			MemberIP:        CurrentMember.IP,
			MemberState:     CurrentMember.State,
		}
		TTLCaches.Set(&reply_update)
		isUpdateDuplicate(uid)
		Logger.Info("Introducer set its info update to the cache\n")
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
	// Read and insert new member to the member list
	var member Member
	buf := bytes.NewReader(payload)
	err := binary.Read(buf, binary.BigEndian, &member)
	printError(err)
	CurrentList.Insert(&member)
	addUpdate2Cache(&member, UpdateTypeJoin)

	// Prepare the entire member list as payload
	var binBuffer bytes.Buffer
	for i := 0; i < CurrentList.Size(); i++ {
		member_, _ := CurrentList.RetrieveByIdx(i)
		binary.Write(&binBuffer, binary.BigEndian, member_)
	}

	// Send Init Reply
	packet := Header{MessageTypeMemInitReply, seq, 0}
	var msgBuffer bytes.Buffer
	binary.Write(&msgBuffer, binary.BigEndian, packet)
	msgBuffer.Write(binBuffer.Bytes())

	udpSend(addr+Port, msgBuffer.Bytes())
}

func initRequest(member *Member) {
	// Construct Init Request payload
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, member)

	// Send Init Request
	packet := Header{MessageTypeMemInitRequest, 0, 0}
	var msgBuffer bytes.Buffer
	binary.Write(&msgBuffer, binary.BigEndian, packet)
	msgBuffer.Write(binBuffer.Bytes())

	udpSend(IntroducerIP+Port, msgBuffer.Bytes())

	// Start Init timer
	init_timer = time.NewTimer(InitTimeoutPeriod)
	go func() {
		<-init_timer.C
		Logger.Info("Init %s timeout, process exit\n", IntroducerIP)
		os.Exit(1)
	}()
}

func ackWithPayload(addr string, seq uint16, payload []byte, reserved uint8) {
	packet := Header{MessageTypeAck, seq, reserved}
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, packet)

	if payload != nil {
		binBuffer.Write(payload) // Append payload
	}
	udpSend(addr+Port, binBuffer.Bytes())
}

func ack(addr string, seq uint16, reserved uint8) {
	ackWithPayload(addr, seq, nil, reserved)
}

func pingWithPayload(member *Member, payload []byte) {
	// Generate a sequence number
	seq := uint16(rand.Uint32())
	addr := int2ip(member.IP).String() + Port

	packet := Header{MessageTypePing, seq, 0}
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, packet)

	if payload != nil {
		binBuffer.Write(payload) // Append payload
	}
	udpSend(addr, binBuffer.Bytes())
	Logger.Info("Ping (%s, %d)\n", addr, seq)

	timer := time.NewTimer(PingTimeoutPeriod)
	PingAckTimeout[seq] = timer
	go func() {
		<-timer.C
		Logger.Info("Ping (%s, %d) timeout\n", addr, seq)
		handlePingTimeout(member)
		delete(PingAckTimeout, seq)
	}()
}

func handlePingTimeout(member *Member) {
	if !suspectOn {
		// Suspicion mechanism is disabled, directly mark node as failed
		fmt.Printf("[FAILURE DETECTED] Node with IP: %s and Timestamp: %d is marked as failed directly.\n",
			int2ip(member.IP).String(), member.TimeStamp)
		Logger.Info("[FAILURE DETECTED] Node with IP: %s and Timestamp: %d is marked as failed directly.\n",
			int2ip(member.IP).String(), member.TimeStamp)

		// Delete the node
		err := CurrentList.Delete(member.TimeStamp, member.IP)
		printError(err)
	} else {
		// Suspicion mechanism is enabled, mark node as suspect
		err := CurrentList.Update(member.TimeStamp, member.IP, StateSuspect)
		if err == nil {
			fmt.Printf("[SUSPECTED NODE] Node with IP: %s and Timestamp: %d is suspected by this node\n",
				int2ip(member.IP).String(), member.TimeStamp)
			Logger.Info("[SUSPECTED NODE] Node with IP: %s and Timestamp: %d is suspected by this node\n",
				int2ip(member.IP).String(), member.TimeStamp)

			// Add suspect update to TTL cache and broadcast
			addUpdate2Cache(member, UpdateTypeSuspect)
		}

		// Set a local suspicion timeout timer, after which the node is marked as failed
		failure_timer := time.NewTimer(SuspectPeriod)
		FailureTimeout[[2]uint64{member.TimeStamp, uint64(member.IP)}] = failure_timer
		go func() {
			<-failure_timer.C
			Logger.Info("[Failure Detected](%s, %d) Failed, detected by self\n", int2ip(member.IP).String(), member.TimeStamp)
			err := CurrentList.Delete(member.TimeStamp, member.IP)
			printError(err)
			delete(FailureTimeout, [2]uint64{member.TimeStamp, uint64(member.IP)})
		}()
	}
}

func ping(member *Member) {
	pingWithPayload(member, nil)
}

// Broadcast suspicion mechanism status change
func broadcastFlagChange(flagStatus bool) {
	// Convert status to uint8 (1 for enabled, 0 for disabled)
	flagValue := uint8(0)
	if flagStatus {
		flagValue = 1
	}

	// Send FlagUpdate message to all other members
	for _, member := range CurrentList.Members {
		if member != nil && member.IP != CurrentMember.IP {
			Logger.Info("Sending FlagUpdate to %s", int2ip(member.IP).String())
			sendFlagUpdate(member, flagValue)
		}
	}
}

// Send FlagUpdate message
func sendFlagUpdate(member *Member, flagValue uint8) {
	// Construct the FlagUpdate message
	update := Update{
		UpdateID:        TTLCaches.RandGen.Uint64(),
		TTL:             TTL_,
		UpdateType:      0, // Not used for FlagUpdate
		MemberTimeStamp: CurrentMember.TimeStamp,
		MemberIP:        CurrentMember.IP,
		MemberState:     flagValue, // Store suspicion mechanism status in MemberState
	}

	// Include a header in the message
	packet := Header{MessageTypeFlagUpdate, 0, 0}
	var binBuffer bytes.Buffer
	binary.Write(&binBuffer, binary.BigEndian, packet)
	binary.Write(&binBuffer, binary.BigEndian, &update)

	// Send the FlagUpdate message
	udpSend(int2ip(member.IP).String()+Port, binBuffer.Bytes())
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
	if initilize() {
		fmt.Printf("[INFO]: Start service\n")
	}
	// Start daemon
	udpDaemon()
}
