package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"github.com/bobertlo/go-mpg123/mpg123"
	"github.com/gordonklaus/portaudio"
	"fmt"
	"net/http"
	"io/ioutil"
	"log"
	"net"
	"time"
	"strings"
	"reflect"
	"github.com/robfig/cron"
	"os/signal"
)

const (
	MsgTypeC2S = uint16(689)
)

type DyProtocol struct {
	length   uint32
	msgType  uint16
	encrypt  uint8
	reserved uint8
	data     string
}

type MessageBody struct {
	MsgType string
	Uid     string
	Level   string
	Nn      string
	Txt     string
	Bnn     string
	Bl      string
}

var ProtocolMapping = map[string]string{
	"type":  "MsgType",
	"uid":   "Uid",
	"nn":    "Nn",
	"level": "Level",
	"txt":   "Txt",
	"bnn":   "Bnn",
	"bl":    "Bl",
}

func newDyProtocol(data string, msgType uint16) *DyProtocol {
	return &DyProtocol{0, msgType, uint8(0), uint8(0), data}
}

func (p *DyProtocol) serialize() []byte {
	dataBytes := []byte(p.data)
	msgLenBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(msgLenBytes, uint32(len(dataBytes)+9))

	buffer := bytes.NewBuffer([]byte{})
	buffer.Write(msgLenBytes)
	buffer.Write(msgLenBytes)

	binary.Write(buffer, binary.LittleEndian, p.msgType)
	binary.Write(buffer, binary.LittleEndian, p.encrypt)
	binary.Write(buffer, binary.LittleEndian, p.reserved)
	// writes message body
	buffer.Write(dataBytes)
	// message body end
	binary.Write(buffer, binary.LittleEndian, uint8(0))
	return buffer.Bytes()
}

var currentBarrage = ""

func main() {

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	roomId := os.Args[1:][0]
	conn := dialServer()
	defer logout(conn)

	loginRsp := login(conn, roomId)
	if len(loginRsp) <= 0 {
		log.Panic("Login Barrage Server Failed!!!!")
	}

	log.Print("Login Success!")
	joinGroup(conn, roomId)
	log.Print("Join Group Success!")

	go heartbeat(conn)

	c := cron.New()
	c.AddFunc("*/10 * * * * ?", func() {
		if len(currentBarrage) > 0 {
			fmt.Println("Playing", currentBarrage)
			go play(downloadMp3(buildText2AudioUrl(currentBarrage)))
			currentBarrage = ""
		}
	})

	go readAndSet(conn)

	c.Start()

	for {
		select {
		case <-sig:
			return
		default:
		}
	}
}

func readAndSet(conn net.Conn) {
	for {
		msg, err := readMessage(conn, 5*time.Second)
		if nil != err {
			log.Fatal(err)
		}
		if len(msg) > 0 {
			message := decodeMessage(msg)
			if strings.Compare("chatmsg", message.MsgType) == 0 {
				//log.Printf("UserId: %10s, UserName:%s, UserLvl: %s, Bnn: %s, BnLvl:%s, Txt:%s",
				//	message.Uid, message.Nn, message.Level, message.Bnn, message.Bl, message.Txt)

				log.Printf("UserId: %10s, UserName:%s, Txt:%s",
					message.Uid, message.Nn, message.Txt)
				if !strings.Contains(message.Txt, "emot") {
					currentBarrage = message.Txt
				}
			}
		}
	}
}

func buildText2AudioUrl(text string) string {
	return fmt.Sprintf(
		"https://tsn.baidu.com/text2audio?tex=%s&tok=24.021882009758c35731834fc5c2542752.2592000.1525273569.282335-11038234&cuid=201804022309&ctp=1&lan=zh&spd=5&per=4",
		text)
}

func downloadMp3(url string) string {
	fileName := "/Users/Sam/temp/test.mp3"
	resp, err := http.Get(url)
	if err != nil {
		log.Panic("Get mp3 file error", url, err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Panic("Read mp3 file failed", err)
	}
	ioutil.WriteFile(fileName, body, os.ModePerm)
	return fileName
}

func chk(err error) {
	if err != nil {
		panic(err)
	}
}

func play(file string) {
	//sig := make(chan os.Signal, 1)
	//signal.Notify(sig, os.Interrupt, os.Kill)

	// create mpg123 decoder instance
	decoder, err := mpg123.NewDecoder("")
	chk(err)

	chk(decoder.Open(file))
	defer decoder.Close()

	// get audio format information
	rate, channels, _ := decoder.GetFormat()

	// make sure output format does not change
	decoder.FormatNone()
	decoder.Format(rate, channels, mpg123.ENC_ANY)

	portaudio.Initialize()
	defer portaudio.Terminate()
	out := make([]int16, 1024)
	stream, err := portaudio.OpenDefaultStream(0, channels, float64(rate), len(out), &out)
	if nil != err {
		return
	}
	defer stream.Close()

	chk(stream.Start())
	defer stream.Stop()
	for {
		audio := make([]byte, 2*len(out))
		_, err = decoder.Read(audio)
		if err == mpg123.EOF {
			break
		}
		chk(err)
		chk(binary.Read(bytes.NewBuffer(audio), binary.LittleEndian, out))
		err := stream.Write()
		if nil != err {
			return
		}
	}
}

func dialServer() net.Conn {
	conn, err := net.Dial("tcp", "openbarrage.douyutv.com:8601")
	if nil != err {
		log.Panic(err)
	}
	return conn
}

func login(conn net.Conn, roomId string) string {
	conn.Write(newDyProtocol(fmt.Sprintf("type@=loginreq/roomid@=%s/", roomId), MsgTypeC2S).serialize())
	msg, err := readMessage(conn, 5*time.Second)
	if nil != err {
		log.Panic(err)
	}
	return msg
}

func logout(conn net.Conn) {
	conn.Write(newDyProtocol("type@=logout/", MsgTypeC2S).serialize())
	conn.Close()
}

func joinGroup(conn net.Conn, roomId string) {
	conn.Write(newDyProtocol(fmt.Sprintf("type@=joingroup/gid@=-9999/rid@=%s/", roomId), MsgTypeC2S).serialize())
}

func readMessage(conn net.Conn, d time.Duration) (msg string, err error) {

	// read first 4 bytes
	first4bytes := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(d))
	conn.Read(first4bytes)
	msgBytesCount := binary.LittleEndian.Uint32(first4bytes)
	if msgBytesCount == 0 {
		time.Sleep(1 * time.Second)
		return "", nil
	}
	//log.Print("message length ", msgBytesCount)

	msgBody := make([]byte, msgBytesCount)
	conn.SetReadDeadline(time.Now().Add(d))
	count, err := conn.Read(msgBody)
	//log.Print("message read length ", count)
	if uint32(count) != msgBytesCount {
		//log.Fatal(err)
		return "", nil
	}
	if count <= 8 {
		return "", err
	}
	if nil != err {
		return "", err
	}
	return string(msgBody[8: count-1]), nil
}

func heartbeat(conn net.Conn) {
	for {
		time.Sleep(45 * time.Second)
		conn.Write(newDyProtocol("type@=mrkl/", MsgTypeC2S).serialize())
		log.Print("Heartbeat Sent")
	}
}

func decodeMessage(message string) MessageBody {
	kvs := strings.Split(message, "/")
	mb := MessageBody{}
	for _, kv := range kvs {
		entry := strings.Split(kv, "@=")
		if len(entry) != 2 {
			continue
		}
		if mappedField, ok := ProtocolMapping[entry[0]]; ok {
			reflect.Indirect(reflect.ValueOf(&mb)).FieldByName(mappedField).SetString(entry[1])
		}
	}
	return mb
}
