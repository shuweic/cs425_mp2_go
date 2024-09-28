package main

import (
	"log"
	"os"
)

// Logger struct
type ssmsLogger struct {
	infoLogger  *log.Logger
	debugLogger *log.Logger
	errorLogger *log.Logger
}

// Return a new logger with argument filepath and identity str
func NewSsmsLogger(id string) *ssmsLogger {
	mylogger := ssmsLogger{}
	file, _ := os.OpenFile("./ssms.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
	commonPrefix := "**" + id + "** "
	prefix := "[INFO]: "
	mylogger.infoLogger = log.New(file, prefix+commonPrefix, log.Ldate|log.Lmicroseconds)
	prefix = "[DEBUG]: "
	mylogger.debugLogger = log.New(file, prefix+commonPrefix, log.Ldate|log.Lmicroseconds)
	prefix = "[ERROR]: "
	mylogger.errorLogger = log.New(file, prefix+commonPrefix, log.Ldate|log.Lmicroseconds)
	return &mylogger
}

func (sl *ssmsLogger) Info(s string, args ...interface{}) {
	sl.infoLogger.Printf(s, args...)
}

func (sl *ssmsLogger) Debug(s string, args ...interface{}) {
	sl.debugLogger.Printf(s, args...)
}

func (sl *ssmsLogger) Error(s string, args ...interface{}) {
	sl.errorLogger.Printf(s, args...)
}

//func main() {
//logger := NewSsmsLogger("10.0.0.1")
//for s := 0; s < 1000; s += 1 {
//logger.Debug("How about you\n")
//}
/*}*/
