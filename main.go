// 이 파일은 실행 가능한 프로그램(main)의 코드라는 선언입니다.
package main

// 이 코드에서 가져다 쓸 도구(라이브러리) 목록입니다.
import (
	"crypto/rand"  // 예측 불가능한 진짜 무작위 값을 만드는 도구입니다. (토큰 생성용, Go 기본 내장)
	"encoding/hex" // 바이트 데이터를 읽기 쉬운 16진수 글자로 바꾸는 도구입니다. (Go 기본 내장)
	"io"           // 데이터를 읽는 기본 도구입니다.
	"log"          // 화면(터미널)에 기록을 남기는 도구입니다.
	"math"         // 수학 도구입니다. (좌표 검사, 거리 계산용, Go 기본 내장)
	"net/http"     // 웹 서버(HTTP)를 만드는 기본 도구입니다.
	"os"           // 운영체제 환경변수를 읽는 도구입니다. (서버 주소 설정용, Go 기본 내장)
	"strings"      // 글자를 다듬고 비교하는 도구입니다. (Authorization 헤더 해석용, Go 기본 내장)
	"sync"         // 여러 접속이 동시에 같은 데이터를 건드릴 때 줄 세우는 도구입니다.
	"time"         // 시간(타이머, 제한시간, 토큰 만료 등)을 다루는 도구입니다.

	"github.com/gorilla/websocket"     // 실시간 통신(WebSocket) 외부 라이브러리입니다.
	"google.golang.org/protobuf/proto" // 패킷 포장/개봉 외부 라이브러리입니다.
)

// ===================== 설정 상수(바뀌지 않는 값) =====================

// 설정값들을 한 묶음으로 선언합니다.
const (
	maxRoomPlayers     = 4                      // 방 하나에 들어갈 수 있는 최대 인원입니다. (재접속 대기 중인 사람도 자리를 차지합니다)
	writeTimeout       = 3 * time.Second        // 한 명에게 패킷을 보낼 때 기다려 주는 최대 시간입니다.
	maxPacketSize      = 4096                   // 웹소켓으로 받을 수 있는 패킷 1개의 최대 크기(바이트)입니다.
	maxDeviceIDLength  = 128                    // 기기 ID의 최대 글자 수입니다.
	tickInterval       = 100 * time.Millisecond // 스냅샷 방송 간격입니다. 100ms = 1초에 10번(10Hz).
	graceDuration      = 15 * time.Second       // 연결이 갑자기 끊긴 유저의 자리를 비워 두고 기다려 주는 시간입니다.
	pingInterval       = 15 * time.Second       // 서버가 "살아 있나요?" 확인 신호를 보내는 간격입니다.
	pongWait           = 45 * time.Second       // 이 시간 동안 아무 응답이 없으면 연결이 죽은 것으로 판단합니다.
	sendQueueSize      = 64                     // 접속자 한 명당 보낼 패킷을 쌓아 두는 대기열 크기입니다.
	maxCoordinate      = 100000.0               // 이보다 큰 좌표값은 비정상으로 보고 버립니다.
	maxMoveSpeed       = 7.0                    // [안티치트] 서버가 허용하는 초당 최대 이동 속도(칸/초)입니다.
	moveBurst          = 2.0                    // [안티치트] 이동 통장에 쌓일 수 있는 최대 잔액(칸)입니다.
	correctionCooldown = 200 * time.Millisecond // [안티치트] 위치 보정 패킷을 보내는 최소 간격입니다.
	maxViolations      = 50                     // [안티치트] 연속으로 이만큼 거절되면 서버가 접속을 끊습니다.

	tokenTTL           = 1 * time.Hour          // [보안] 세션 토큰의 유효 시간입니다. (24시간으로 하려면 24 * time.Hour. 만료 테스트 땐 30 * time.Second)
	tokenCleanInterval = 5 * time.Minute        // [보안] 만료된 토큰을 장부에서 청소하는 간격입니다.
	allowQueryToken    = true                   // [보안] true 면 헤더가 없을 때 ?token=... 방식도 허용합니다. 상용 배포 때는 false 로 바꾸세요.
	defaultListenAddr  = ":8080"                // 서버가 접속을 기다릴 기본 주소입니다. 클라우드에서는 환경변수로 127.0.0.1:8080 을 지정하세요.
)

// ===================== 유저 장부 (로그인 정보) =====================

// 유저 한 명의 로그인 기록 양식입니다.
type PlayerRecord struct {
	PlayerID     int64     // 이 유저의 플레이어 번호입니다.
	SessionToken string    // 이 유저에게 발급된 현재 입장 팔찌(토큰)입니다.
	ExpiresAt    time.Time // [보안] 이 토큰이 만료되는 시각입니다. 이 시각이 지나면 토큰은 무효입니다.
}

// 로그인한 유저 정보를 보관하는 변수 묶음입니다.
var (
	playersMutex  sync.Mutex                 // 유저 장부를 한 번에 한 명씩만 고치게 하는 "열쇠"입니다.
	devicePlayers = map[string]*PlayerRecord{} // 기기ID -> 유저 기록(번호+토큰+만료시각)을 적어 두는 장부입니다.
	tokenOwners   = map[string]*PlayerRecord{} // 토큰 -> 그 토큰의 주인 기록을 적어 두는 "팔찌 확인용" 장부입니다. (같은 기록을 가리킵니다)
	nextPlayerID  int64                  = 1 // 다음 신규 유저에게 줄 번호입니다. 1부터 시작합니다.
)

// ===================== 접속자(Client) =====================

// 웹소켓으로 연결된 접속자 한 명의 정보 양식입니다.
type Client struct {
	conn      *websocket.Conn // 이 접속자와 연결된 실시간 통로입니다.
	playerID  int64           // 토큰 검증으로 확인된, 이 접속자의 진짜 플레이어 번호입니다.
	send      chan []byte     // 이 접속자에게 보낼 패킷을 쌓아 두는 "우편함(대기열)"입니다.
	done      chan struct{}   // 이 접속자가 끝났다는 신호를 알리는 통로입니다. (닫히면 종료)
	closeOnce sync.Once       // 종료 처리를 딱 한 번만 실행하게 보장하는 도구입니다.
}

// 새 접속자 정보를 만들어 돌려주는 함수입니다.
func newClient(conn *websocket.Conn, playerID int64) *Client {
	return &Client{
		conn:     conn,                             // 실시간 통로를 담습니다.
		playerID: playerID,                         // 플레이어 번호를 담습니다.
		send:     make(chan []byte, sendQueueSize), // 우편함을 만듭니다.
		done:     make(chan struct{}),              // 종료 신호 통로를 만듭니다.
	}
}

// 이 접속자에게 패킷을 "우편함에 넣는" 함수입니다. 절대 기다리지 않습니다. (방 전체가 멈추는 것 방지)
func (c *Client) Enqueue(data []byte) {
	// 이미 종료된 접속자라면
	select {
	case <-c.done: // 종료 신호가 와 있으면
		return // 보내지 않고 끝냅니다.
	default: // 아직 종료되지 않았다면 아래로 계속 진행합니다.
	}
	// 우편함에 넣어 봅니다.
	select {
	case c.send <- data: // 우편함에 자리가 있으면 넣습니다.
	default: // 우편함이 가득 찼다면 (이 사람의 네트워크가 너무 느린 경우)
		log.Printf("플레이어 %d 의 대기열이 가득 차서 패킷 1개를 버립니다.", c.playerID) // 버렸다고 기록합니다.
	}
}

// 이 접속자를 완전히 종료하는 함수입니다. 여러 번 호출해도 한 번만 실행됩니다.
func (c *Client) Close() {
	c.closeOnce.Do(func() { // 딱 한 번만 실행합니다.
		close(c.done)  // 종료 신호를 보냅니다. (배달 고루틴이 이 신호를 보고 끝납니다)
		c.conn.Close() // 실제 연결을 닫습니다.
	})
}

// 우편함에 쌓인 패킷을 실제로 배달하는 전용 고루틴입니다. 접속자마다 하나씩 돌아갑니다.
// 웹소켓은 "동시에 두 명이 쓰기"를 금지하므로, 쓰기는 반드시 이 고루틴 한 곳에서만 합니다.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval) // 15초마다 울리는 알람을 만듭니다. (살아 있는지 확인용)
	defer ticker.Stop()                    // 함수가 끝나면 알람을 끕니다.
	// 끝날 때까지 계속 반복합니다.
	for {
		select {
		case data := <-c.send: // 우편함에 배달할 패킷이 들어왔다면
			c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)) // 배달 제한시간을 정합니다.
			// 패킷을 실제로 보냅니다.
			if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				c.Close() // 보내지 못하면 연결이 죽은 것이므로 종료합니다.
				return    // 배달 고루틴을 끝냅니다.
			}
		case <-ticker.C: // 확인 신호를 보낼 시간이 되었다면
			c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)) // 제한시간을 정합니다.
			// "살아 있나요?" 신호(Ping)를 보냅니다. 상대가 살아 있으면 자동으로 응답(Pong)합니다.
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Close() // 실패하면 종료합니다.
				return    // 배달 고루틴을 끝냅니다.
			}
		case <-c.done: // 종료 신호가 왔다면
			return // 배달 고루틴을 끝냅니다.
		}
	}
}

// ===================== 방(Room) 과 칠판(State) =====================

// 방 안에 있는 플레이어 한 명의 "칠판 기록" 양식입니다.
type Member struct {
	playerID   int64       // 이 사람의 플레이어 번호입니다.
	x, y, z    float32     // 이 사람의 가장 최근 "검증을 통과한" 위치입니다. (서버가 기억하는 중앙 상태)
	client     *Client     // 현재 연결된 접속자입니다. 연결이 끊겨 재접속을 기다리는 중이면 nil(비어 있음)입니다.
	graceTimer *time.Timer // 재접속을 기다리는 동안 돌아가는 유예 타이머입니다. (정상 연결 중에는 nil)

	hasPosition    bool      // [안티치트] 첫 위치를 이미 받았는지 나타냅니다. false 면 다음 좌표를 "시작 위치"로 인정합니다.
	lastMoveTime   time.Time // [안티치트] 마지막으로 정상 이동이 확인된 시각입니다.
	lastCheckTime  time.Time // [안티치트] 마지막으로 이동 패킷을 검사한 시각입니다. (통장 입금 계산용)
	moveBudget     float64   // [안티치트] "이동 통장" 잔액입니다. 시간이 지나면 쌓이고, 움직이면 줄어듭니다.
	violations     int       // [안티치트] 연속으로 거절된 횟수입니다. 정상 이동이 한 번 통과하면 0으로 돌아갑니다.
	lastCorrection time.Time // [안티치트] 마지막으로 위치 보정 패킷을 보낸 시각입니다.
}

// 방 하나의 정보 양식입니다.
type Room struct {
	id      int               // 방 번호입니다. (로그 확인용)
	mu      sync.Mutex        // 이 방의 칠판을 한 번에 한 명씩만 고치게 하는 "방 전용 열쇠"입니다.
	members map[int64]*Member // 방에 속한 사람들의 칠판입니다. (플레이어 번호 -> 기록)
	dirty   bool              // "마지막 방송 이후 칠판이 바뀌었나요?" 표시입니다. 바뀐 게 없으면 방송을 건너뜁니다.
	stop    chan struct{}     // 방이 없어질 때 스냅샷 방송 고루틴에게 "그만해"라고 알리는 통로입니다.
}

// 방들을 관리하는 관리자(Room Manager) 양식입니다.
type RoomManager struct {
	mu          sync.Mutex      // 방 목록 자체를 고칠 때 쓰는 "관리자 열쇠"입니다. (열쇠 순서: 관리자 열쇠 -> 방 열쇠)
	rooms       []*Room         // 현재 존재하는 모든 방의 목록입니다. 먼저 만들어진 방이 앞에 있습니다.
	playerRooms map[int64]*Room // 플레이어 번호 -> 그 사람이 속한 방입니다. (재접속 때 원래 방을 찾는 용도)
	nextRoomID  int             // 다음에 만들 방에 붙일 번호입니다.
}

// 서버 전체에서 하나만 쓰는 룸 관리자입니다.
var roomManager = &RoomManager{
	playerRooms: map[int64]*Room{}, // 빈 장부를 만들어 둡니다.
	nextRoomID:  1,                 // 방 번호는 1부터 시작합니다.
}

// 현재 연결 중인 접속자를 플레이어 번호별로 기억해 두는 변수 묶음입니다. (중복 접속 차단용)
var (
	activeMutex   sync.Mutex            // 이 명단을 고칠 때 쓰는 열쇠입니다.
	activeClients = map[int64]*Client{} // 플레이어 번호 -> 현재 연결된 접속자입니다.
)

// 서버 패킷(ServerMessage)을 전송 가능한 바이너리로 "포장"하는 도우미 함수입니다.
func marshalServerMessage(msg *ServerMessage) []byte {
	out, err := proto.Marshal(msg) // 포장을 시도합니다.
	// 포장에 실패했다면
	if err != nil {
		log.Println("서버 패킷 포장 실패:", err) // 오류를 기록합니다.
		return nil                          // 빈 값을 돌려줍니다.
	}
	return out // 포장된 데이터를 돌려줍니다.
}

// 방 칠판 전체를 사진 찍어 RoomSnapshot 패킷으로 만드는 함수입니다. (호출하는 쪽이 방 열쇠를 이미 쥐고 있어야 합니다)
func (r *Room) snapshotLocked() []byte {
	players := make([]*PlayerState, 0, len(r.members)) // 사람 목록을 담을 빈 상자를 준비합니다.
	// 칠판에 적힌 모든 사람을 하나씩 돌아봅니다.
	for _, m := range r.members {
		// 이 사람의 번호와 위치를 목록에 추가합니다.
		players = append(players, &PlayerState{
			PlayerId: m.playerID,                       // 플레이어 번호입니다.
			Position: &Vector3{X: m.x, Y: m.y, Z: m.z}, // 칠판에 적힌 최신 위치입니다.
		})
	}
	// 목록을 스냅샷 패킷으로 만들고 서버 포장지에 담아 포장합니다.
	return marshalServerMessage(&ServerMessage{
		Payload: &ServerMessage_Snapshot{Snapshot: &RoomSnapshot{Players: players}},
	})
}

// 방에서 "연결 중인" 사람들에게 패킷을 우편함에 넣어 주는 함수입니다. (방 열쇠를 쥔 상태에서 호출)
// exceptID 에 적힌 플레이어는 제외합니다. 제외할 사람이 없으면 0 을 넣으세요.
func (r *Room) broadcastLocked(data []byte, exceptID int64) {
	// 칠판의 모든 사람을 돌아봅니다.
	for _, m := range r.members {
		// 제외 대상이거나, 연결이 끊겨 재접속을 기다리는 중이라면
		if m.playerID == exceptID || m.client == nil {
			continue // 건너뜁니다.
		}
		m.client.Enqueue(data) // 이 사람의 우편함에 넣습니다. (기다리지 않으므로 방이 멈추지 않습니다)
	}
}

// 방에 현재 몇 명이 있는지(재접속 대기 중인 사람 포함) 알려 주는 함수입니다.
func (r *Room) Count() int {
	r.mu.Lock()           // 읽는 동안 바뀌지 않게 방 열쇠를 잠급니다.
	defer r.mu.Unlock()   // 함수가 끝나면 자동으로 열쇠를 풉니다.
	return len(r.members) // 현재 인원 수를 돌려줍니다.
}

// [안티치트 핵심] 한 플레이어의 이동 요청을 검증하고, 통과하면 칠판에 적는 함수입니다.
// 돌려주는 값이 true 면 "상습 위반자이므로 서버가 이 사람을 내보내야 한다"는 뜻입니다.
func (r *Room) UpdatePosition(c *Client, x, y, z float32) bool {
	r.mu.Lock()         // 칠판을 읽고 고치기 위해 방 열쇠를 잠급니다.
	defer r.mu.Unlock() // 함수가 끝나면 자동으로 풉니다.

	m, ok := r.members[c.playerID] // 이 사람의 기록을 찾습니다.
	// 기록이 없거나(이미 퇴장), 칠판에 연결된 접속자가 "이 연결"이 아니라면 (재접속으로 교체된 옛 연결)
	if !ok || m.client != c {
		return false // 옛 연결의 패킷은 무시합니다.
	}

	now := time.Now() // 지금 시각을 기록해 둡니다.

	// [첫 위치] 아직 첫 위치를 받은 적이 없다면
	if !m.hasPosition {
		m.x, m.y, m.z = x, y, z  // 이 좌표를 "시작 위치"로 인정하고 칠판에 적습니다.
		m.hasPosition = true     // 이제 첫 위치를 받았다고 표시합니다.
		m.moveBudget = moveBurst // 이동 통장을 가득 채워서 시작합니다.
		m.lastCheckTime = now    // 마지막 검사 시각을 지금으로 정합니다.
		m.lastMoveTime = now     // 마지막 정상 이동 시각도 지금으로 정합니다.
		m.violations = 0         // 위반 횟수를 0으로 둡니다.
		r.dirty = true           // 칠판이 바뀌었으니 다음 방송 때 보내라고 표시합니다.
		return false             // 문제 없으니 끝냅니다.
	}

	// [통장 입금] 지난 검사 이후 흐른 시간(초)을 계산합니다.
	elapsed := now.Sub(m.lastCheckTime).Seconds()
	m.lastCheckTime = now // 이번 검사 시각을 기록합니다.
	// 흐른 시간만큼 초당 최대 속도어치를 통장에 입금합니다.
	m.moveBudget += maxMoveSpeed * elapsed
	// 통장 잔액이 상한(moveBurst)을 넘었다면
	if m.moveBudget > moveBurst {
		m.moveBudget = moveBurst // 상한까지만 인정합니다.
	}

	// [거리 계산] 칠판에 적힌 마지막 정상 위치에서 새 좌표까지의 3D 직선 거리를 구합니다.
	dx := float64(x) - float64(m.x)          // 좌우 차이입니다.
	dy := float64(y) - float64(m.y)          // 높이 차이입니다.
	dz := float64(z) - float64(m.z)          // 앞뒤 차이입니다.
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz) // 피타고라스 공식으로 거리를 구합니다.

	// [판정] 이동 거리가 통장 잔액 이내라면 정상 이동입니다.
	if dist <= m.moveBudget {
		m.moveBudget -= dist    // 이동한 거리만큼 통장에서 출금합니다.
		m.x, m.y, m.z = x, y, z // 칠판에 새 좌표를 적습니다.
		m.lastMoveTime = now    // 마지막 정상 이동 시각을 갱신합니다.
		m.violations = 0        // 연속 위반 횟수를 0으로 되돌립니다.
		r.dirty = true          // 칠판이 바뀌었으니 다음 방송 때 보내라고 표시합니다.
		return false            // 문제 없으니 끝냅니다.
	}

	// ===== 여기부터는 "잔액 부족 = 스피드핵 또는 순간이동 의심" 구간입니다 =====
	m.violations++ // 연속 위반 횟수를 1 올립니다. (칠판 좌표는 갱신하지 않고 요청을 무시합니다)
	log.Printf("[안티치트] 플레이어 %d 이동 거부: 거리=%.2f, 허용=%.2f, 연속 위반=%d회", c.playerID, dist, m.moveBudget, m.violations)

	// [위치 보정] 마지막 보정으로부터 충분한 시간이 지났다면
	if now.Sub(m.lastCorrection) >= correctionCooldown {
		m.lastCorrection = now // 보정 패킷을 보낸 시각을 기록합니다.
		// "정상 좌표로 돌아가라"는 패킷을 만듭니다.
		correction := marshalServerMessage(&ServerMessage{
			Payload: &ServerMessage_Correction{Correction: &PositionCorrection{
				Position: &Vector3{X: m.x, Y: m.y, Z: m.z}, // 서버 칠판에 적힌 마지막 정상 좌표입니다.
			}},
		})
		// 포장에 성공했다면
		if correction != nil {
			c.Enqueue(correction) // 위반한 본인의 우편함에만 넣습니다.
		}
	}

	// 연속 위반 횟수가 한도를 넘었다면 true(내보내야 함), 아니라면 false 를 돌려줍니다.
	return m.violations >= maxViolations
}

// 방마다 독립적으로 돌아가는 "스냅샷 방송 고루틴"입니다. 방이 만들어질 때 시작됩니다.
func (r *Room) run() {
	ticker := time.NewTicker(tickInterval) // 0.1초마다 울리는 알람을 만듭니다. (1초에 10번)
	defer ticker.Stop()                    // 함수가 끝나면 알람을 끕니다.
	// 방이 사라질 때까지 계속 반복합니다.
	for {
		select {
		case <-ticker.C: // 알람이 울렸다면 (방송 시간)
			r.mu.Lock() // 칠판을 읽는 동안 방 열쇠를 잠급니다.
			// 마지막 방송 이후 칠판이 바뀌었다면
			if r.dirty {
				r.dirty = false            // 바뀜 표시를 끕니다.
				snap := r.snapshotLocked() // 칠판 전체를 사진 찍어 패킷으로 만듭니다.
				// 사진 촬영에 성공했다면
				if snap != nil {
					r.broadcastLocked(snap, 0) // 방의 모든 연결자에게 묶음 배송합니다.
				}
			}
			r.mu.Unlock() // 방 열쇠를 풉니다.
		case <-r.stop: // 방이 없어졌다는 신호가 왔다면
			return // 방송 고루틴을 끝냅니다.
		}
	}
}

// 접속자를 방에 배정하는 함수입니다. 재접속이면 원래 방의 원래 자리로 되돌립니다.
// 두 번째 반환값이 true 면 "재접속으로 복귀했다"는 뜻입니다.
func (rm *RoomManager) Join(c *Client) (*Room, bool) {
	rm.mu.Lock()         // 방 목록을 훑는 동안 다른 변경을 막기 위해 관리자 열쇠를 잠급니다.
	defer rm.mu.Unlock() // 함수가 끝나면 자동으로 풉니다.

	// [재접속 확인] 이 플레이어가 원래 속해 있던 방이 있는지 찾아봅니다.
	if room, ok := rm.playerRooms[c.playerID]; ok {
		room.mu.Lock() // 방 열쇠를 잠급니다.
		// 칠판에 이 사람의 자리가 아직 남아 있다면 (유예 시간 안에 돌아온 경우)
		if m, exists := room.members[c.playerID]; exists {
			// 돌아가고 있던 유예 타이머가 있다면
			if m.graceTimer != nil {
				m.graceTimer.Stop() // 타이머를 멈춥니다. (더 이상 추방하지 않습니다)
				m.graceTimer = nil  // 타이머 기록을 지웁니다.
			}
			m.client = c                 // 새 연결을 이 자리에 다시 연결합니다.
			m.violations = 0             // [안티치트] 연속 위반 횟수를 초기화합니다.
			m.moveBudget = moveBurst     // [안티치트] 이동 통장을 가득 채웁니다.
			m.lastCheckTime = time.Now() // [안티치트] 검사 시각을 지금으로 맞춥니다.
			room.dirty = true            // 다른 사람들도 최신 상태를 받도록 표시합니다.
			// 복귀한 사람에게 방의 최신 칠판 사진을 즉시 보내 줍니다.
			if snap := room.snapshotLocked(); snap != nil {
				c.Enqueue(snap) // 우편함에 넣습니다.
			}
			room.mu.Unlock()  // 방 열쇠를 풉니다.
			return room, true // 원래 방으로 복귀했다고 알립니다.
		}
		room.mu.Unlock()                   // 자리가 없었다면 방 열쇠를 풉니다.
		delete(rm.playerRooms, c.playerID) // 낡은 기록이므로 지웁니다.
	}

	// [신규 입장] 자리가 있는 방을 먼저 만들어진 순서대로 찾습니다.
	var target *Room // 배정할 방을 담을 변수입니다.
	for _, room := range rm.rooms {
		room.mu.Lock()                                 // 인원을 확인하기 위해 방 열쇠를 잠급니다.
		hasSpace := len(room.members) < maxRoomPlayers // 자리가 남았는지 확인합니다.
		room.mu.Unlock()                               // 방 열쇠를 풉니다.
		// 자리가 있다면
		if hasSpace {
			target = room // 이 방으로 정합니다.
			break         // 더 찾지 않습니다.
		}
	}

	// 자리가 있는 방이 없다면 새 방을 만듭니다.
	if target == nil {
		target = &Room{
			id:      rm.nextRoomID,       // 방 번호를 붙입니다.
			members: map[int64]*Member{}, // 빈 칠판을 만듭니다.
			stop:    make(chan struct{}), // 방송 중지 신호 통로를 만듭니다.
		}
		rm.nextRoomID++                     // 다음 방을 위해 번호를 1 올립니다.
		rm.rooms = append(rm.rooms, target) // 방 목록에 추가합니다.
		go target.run()                     // 이 방 전용 스냅샷 방송 고루틴을 시작합니다.
		log.Printf("방 %d 가 새로 만들어졌습니다.", target.id) // 기록을 남깁니다.
	}

	target.mu.Lock() // 칠판을 고치기 위해 방 열쇠를 잠급니다.
	// 새 기록을 만듭니다. 위치는 (0,0,0)에서 시작하고, 첫 이동 패킷의 좌표가 "시작 위치"로 인정됩니다.
	member := &Member{playerID: c.playerID, client: c}
	target.members[c.playerID] = member // 칠판에 이 사람을 적습니다.
	rm.playerRooms[c.playerID] = target // "이 사람은 이 방 소속"이라고 장부에 적습니다.
	target.dirty = true                 // 칠판이 바뀌었다고 표시합니다.
	// 기존 사람들에게 "새 사람이 들어왔어요" 알림을 만듭니다.
	enter := marshalServerMessage(&ServerMessage{
		Payload: &ServerMessage_Enter{Enter: &PlayerEnter{
			PlayerId: member.playerID,                                 // 새로 들어온 사람의 번호입니다.
			Position: &Vector3{X: member.x, Y: member.y, Z: member.z}, // 입장 위치입니다.
		}},
	})
	// 알림 포장에 성공했다면
	if enter != nil {
		target.broadcastLocked(enter, c.playerID) // 새로 들어온 본인을 제외한 모두에게 알립니다.
	}
	// 새로 들어온 사람에게 방 전체의 칠판 사진을 즉시 보내 줍니다.
	if snap := target.snapshotLocked(); snap != nil {
		c.Enqueue(snap) // 우편함에 넣습니다.
	}
	target.mu.Unlock()   // 방 열쇠를 풉니다.
	return target, false // 신규 입장이라고 알립니다.
}

// 연결이 갑자기 끊겼을 때 호출됩니다. 방에서 내보내지 않고 "재접속 유예 타이머"를 시작합니다.
func (rm *RoomManager) Detach(room *Room, c *Client) {
	room.mu.Lock()         // 칠판을 고치기 위해 방 열쇠를 잠급니다.
	defer room.mu.Unlock() // 함수가 끝나면 자동으로 풉니다.

	m, ok := room.members[c.playerID] // 이 사람의 기록을 찾습니다.
	// 기록이 없거나, 칠판에 연결된 접속자가 "나"가 아니라면 (이미 새 연결로 교체되었거나 이미 퇴장함)
	if !ok || m.client != c {
		return // 할 일이 없습니다.
	}

	m.client = nil         // 연결이 끊겼으므로 접속자 칸을 비웁니다. (기록과 마지막 위치는 그대로 남깁니다)
	playerID := c.playerID // 타이머 안에서 쓸 수 있게 번호를 복사해 둡니다.
	// graceDuration(15초) 뒤에 실행될 타이머를 시작합니다. 그 전에 돌아오면 Join 에서 이 타이머를 멈춥니다.
	m.graceTimer = time.AfterFunc(graceDuration, func() {
		rm.expire(room, playerID, m) // 15초가 지나도 안 돌아오면 완전히 퇴장시킵니다.
	})
	log.Printf("플레이어 %d 연결 끊김 -> 방 %d 에서 %v 동안 자리를 유지합니다.", playerID, room.id, graceDuration) // 기록을 남깁니다.
}

// 유예 시간이 끝났을 때 타이머가 호출하는 함수입니다. 아직도 돌아오지 않았다면 완전히 퇴장시킵니다.
func (rm *RoomManager) expire(room *Room, playerID int64, m *Member) {
	rm.mu.Lock()           // 방 목록도 고칠 수 있으므로 관리자 열쇠를 먼저 잠급니다.
	defer rm.mu.Unlock()   // 함수가 끝나면 자동으로 풉니다.
	room.mu.Lock()         // 그다음 방 열쇠를 잠급니다. (열쇠 순서: 관리자 -> 방)
	defer room.mu.Unlock() // 함수가 끝나면 자동으로 풉니다.

	// 칠판의 기록이 그 사이 바뀌었거나, 이미 재접속해서 접속자가 연결되어 있다면
	if room.members[playerID] != m || m.client != nil {
		return // 퇴장시키면 안 되므로 아무것도 하지 않습니다.
	}
	log.Printf("플레이어 %d 가 유예 시간 안에 돌아오지 않아 방 %d 에서 퇴장 처리합니다.", playerID, room.id) // 기록을 남깁니다.
	rm.removeLocked(room, playerID)                                                       // 완전히 퇴장시킵니다.
}

// 연결이 "정상적으로 닫혔을 때"(게임 종료 등) 또는 치트로 쫓겨날 때 유예 없이 즉시 퇴장시키는 함수입니다.
func (rm *RoomManager) LeaveNow(room *Room, c *Client) {
	rm.mu.Lock()           // 관리자 열쇠를 잠급니다.
	defer rm.mu.Unlock()   // 함수가 끝나면 자동으로 풉니다.
	room.mu.Lock()         // 방 열쇠를 잠급니다.
	defer room.mu.Unlock() // 함수가 끝나면 자동으로 풉니다.

	m, ok := room.members[c.playerID] // 이 사람의 기록을 찾습니다.
	// 기록이 없거나, 연결된 접속자가 "나"가 아니라면 (이미 새 연결로 교체됨)
	if !ok || m.client != c {
		return // 할 일이 없습니다.
	}
	log.Printf("플레이어 %d 가 방 %d 에서 즉시 퇴장합니다.", c.playerID, room.id) // 기록을 남깁니다.
	rm.removeLocked(room, c.playerID)                                    // 즉시 퇴장시킵니다.
}

// 플레이어를 방에서 완전히 지우는 내부 함수입니다. (관리자 열쇠와 방 열쇠를 모두 쥔 상태에서만 호출)
func (rm *RoomManager) removeLocked(room *Room, playerID int64) {
	m := room.members[playerID] // 지울 기록을 가져옵니다.
	// 유예 타이머가 돌고 있다면
	if m != nil && m.graceTimer != nil {
		m.graceTimer.Stop() // 멈춥니다.
	}
	delete(room.members, playerID)   // 칠판에서 지웁니다.
	delete(rm.playerRooms, playerID) // 소속 장부에서도 지웁니다.
	room.dirty = true                // 칠판이 바뀌었다고 표시합니다.
	// 남은 사람들에게 보낼 "퇴장 알림"을 만듭니다.
	leave := marshalServerMessage(&ServerMessage{
		Payload: &ServerMessage_Leave{Leave: &PlayerLeave{PlayerId: playerID}},
	})
	// 알림 포장에 성공했다면
	if leave != nil {
		room.broadcastLocked(leave, playerID) // 남은 연결자들에게 알립니다. (유니티가 캐릭터를 삭제합니다)
	}
	// 방에 아무도 남지 않았다면
	if len(room.members) == 0 {
		remaining := []*Room{} // 남길 방들을 담을 새 목록입니다.
		// 기존 방 목록을 하나씩 살펴봅니다.
		for _, r := range rm.rooms {
			// 방금 비어버린 방이 아니라면
			if r != room {
				remaining = append(remaining, r) // 남길 목록에 넣습니다.
			}
		}
		rm.rooms = remaining // 새 목록으로 교체합니다.
		close(room.stop)     // 이 방의 스냅샷 방송 고루틴에게 "그만해"라고 알립니다.
		log.Printf("방 %d 가 비어서 삭제되었습니다.", room.id) // 삭제 기록을 남깁니다.
	}
}

// ===================== 웹소켓 업그레이드 도구 =====================

// 일반 HTTP 연결을 WebSocket 연결로 "업그레이드"해 주는 도구입니다.
var upgrader = websocket.Upgrader{
	// 어디서 접속하든 허용합니다. (모바일 앱은 Origin 헤더를 보내지 않으며, 인증은 토큰으로 합니다. 웹 서비스를 한다면 허용 도메인으로 제한하세요.)
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ===================== 토큰 생성·검증 =====================

// 추측이 불가능한 무작위 세션 토큰(입장 팔찌)을 만드는 함수입니다.
func generateToken() (string, error) {
	buf := make([]byte, 16) // 16바이트(128비트)짜리 빈 상자를 준비합니다.
	// 운영체제가 제공하는 안전한 무작위 값으로 상자를 채웁니다.
	if _, err := rand.Read(buf); err != nil {
		return "", err // 실패하면 오류를 돌려줍니다.
	}
	return hex.EncodeToString(buf), nil // 읽기 쉬운 32글자 16진수 문자열로 바꿔 돌려줍니다.
}

// [보안] 토큰이 유효한지(장부에 있고 만료되지 않았는지) 확인하고, 유효하면 주인의 플레이어 번호를 돌려주는 함수입니다.
func validateToken(token string) (int64, bool) {
	playersMutex.Lock()         // 장부를 읽는 동안 열쇠를 잠급니다.
	defer playersMutex.Unlock() // 함수가 끝나면 자동으로 풉니다.

	record, ok := tokenOwners[token] // 장부에서 이 토큰의 주인 기록을 찾습니다.
	// 장부에 없는 토큰(가짜이거나 이미 새 토큰으로 교체됨)이라면
	if !ok {
		return 0, false // 무효입니다.
	}
	// 만료 시각이 지났다면
	if time.Now().After(record.ExpiresAt) {
		delete(tokenOwners, token) // 만료된 토큰을 장부에서 지웁니다.
		// 이 기록이 아직 같은 토큰을 들고 있다면
		if record.SessionToken == token {
			record.SessionToken = "" // 기록에서도 토큰을 비웁니다.
		}
		return 0, false // 무효입니다.
	}
	return record.PlayerID, true // 유효하다면 주인의 번호를 돌려줍니다.
}

// [보안] 만료된 토큰을 주기적으로 장부에서 청소하는 고루틴입니다. (프로그램 시작 시 한 번 실행됩니다)
func tokenJanitor() {
	ticker := time.NewTicker(tokenCleanInterval) // 청소 시간마다 울리는 알람을 만듭니다.
	defer ticker.Stop()                          // 함수가 끝나면 알람을 끕니다.
	// 영원히 반복합니다.
	for range ticker.C {
		now := time.Now()    // 지금 시각을 기록합니다.
		removed := 0         // 지운 개수를 셉니다.
		playersMutex.Lock()  // 장부를 고치기 위해 열쇠를 잠급니다.
		// 팔찌 장부의 모든 항목을 살펴봅니다.
		for token, record := range tokenOwners {
			// 만료되었다면
			if now.After(record.ExpiresAt) {
				delete(tokenOwners, token) // 장부에서 지웁니다.
				// 기록이 아직 같은 토큰을 들고 있다면
				if record.SessionToken == token {
					record.SessionToken = "" // 기록에서도 비웁니다.
				}
				removed++ // 지운 개수를 올립니다.
			}
		}
		playersMutex.Unlock() // 열쇠를 풉니다.
		// 지운 것이 있다면
		if removed > 0 {
			log.Printf("[보안] 만료된 토큰 %d 개를 정리했습니다.", removed) // 기록을 남깁니다.
		}
	}
}

// [보안] 웹소켓 접속 요청에서 토큰을 꺼내는 함수입니다.
// 1순위: Authorization: Bearer <토큰> 헤더 / 2순위(헤더가 아예 없을 때만, 허용 시): 주소의 ?token=... 값
func extractToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization")) // 헤더 값을 꺼내 앞뒤 공백을 지웁니다.
	// 헤더가 있다면
	if header != "" {
		const prefix = "bearer " // "Bearer " 로 시작해야 하는 규칙입니다. (대소문자 구분 없음)
		// 형식이 "Bearer 토큰" 이 맞다면
		if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
			return strings.TrimSpace(header[len(prefix):]) // 뒤쪽의 토큰 부분만 돌려줍니다.
		}
		return "" // 헤더가 있는데 형식이 틀리면 보조 방식으로 넘어가지 않고 인증 실패로 처리합니다.
	}
	// 헤더가 없고 쿼리 방식이 허용되어 있다면 (브라우저/WebGL 테스트 편의용)
	if allowQueryToken {
		return r.URL.Query().Get("token") // 주소 뒤의 ?token=... 값을 돌려줍니다.
	}
	return "" // 그 외에는 토큰이 없는 것으로 봅니다.
}

// 좌표 값 하나가 정상 범위인지 검사하는 함수입니다.
func isValidCoord(v float32) bool {
	f := float64(v) // 계산하기 쉬운 형태로 바꿉니다.
	// 숫자가 아니거나(NaN), 무한대거나, 너무 크면 비정상입니다.
	return !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) <= maxCoordinate
}

// ===================== HTTP 핸들러 =====================

// 서버가 켜져 있는지 브라우저로 확인하기 위한 간단한 안내 화면 함수입니다.
func homeHandler(w http.ResponseWriter, r *http.Request) {
	// 화면에 서버 동작 중이라는 글자를 보여 줍니다.
	w.Write([]byte("게임 서버가 정상 작동 중입니다!"))
}

// 로그인 요청(HTTP)을 처리하는 함수입니다. 유니티가 /login 주소로 보냅니다. (로그인은 토큰 없이 기기 ID로 하므로 401 을 내지 않습니다)
func loginHandler(w http.ResponseWriter, r *http.Request) {
	// 로그인은 데이터를 "보내는" 방식(POST)만 허용합니다.
	if r.Method != http.MethodPost {
		// 다른 방식이면 "허용되지 않는 방식"이라고 돌려보냅니다.
		http.Error(w, "POST 방식만 사용할 수 있습니다", http.StatusMethodNotAllowed)
		return // 여기서 함수를 끝냅니다.
	}

	// 유니티가 보낸 데이터를 읽습니다. 최대 1MB까지만 읽어 서버를 보호합니다.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	// 읽다가 문제가 생겼다면
	if err != nil {
		// "잘못된 요청"이라고 알려 줍니다.
		http.Error(w, "요청을 읽을 수 없습니다", http.StatusBadRequest)
		return // 함수를 끝냅니다.
	}

	// 받은 데이터를 담을 빈 로그인 요청 양식을 준비합니다.
	req := &LoginRequest{}
	// 받은 데이터를 로그인 요청 양식으로 "개봉"합니다.
	if err := proto.Unmarshal(body, req); err != nil {
		// 양식에 맞지 않는 데이터면 거절합니다.
		http.Error(w, "패킷 형식이 올바르지 않습니다", http.StatusBadRequest)
		return // 함수를 끝냅니다.
	}

	// 유니티에 돌려줄 로그인 응답 양식을 준비합니다.
	res := &LoginResponse{}

	// 디바이스 ID가 비어 있으면
	if req.DeviceId == "" {
		res.Success = false                    // 실패로 표시합니다.
		res.Message = "디바이스 ID가 비어 있습니다" // 실패 이유를 적습니다.
	} else if len(req.DeviceId) > maxDeviceIDLength {
		// 디바이스 ID가 비정상적으로 길다면
		res.Success = false                   // 실패로 표시합니다.
		res.Message = "디바이스 ID가 너무 깁니다" // 실패 이유를 적습니다.
	} else {
		// 새 입장 팔찌(세션 토큰)를 만듭니다. (열쇠를 잠그기 전에 미리 만들어 열쇠를 오래 쥐지 않게 합니다)
		token, tokenErr := generateToken()
		// 토큰 생성에 실패했다면
		if tokenErr != nil {
			// 서버 내부 오류라고 알려 줍니다.
			http.Error(w, "토큰 생성 실패", http.StatusInternalServerError)
			return // 함수를 끝냅니다.
		}

		// 유저 장부를 고치는 동안 다른 접속이 끼어들지 못하게 열쇠를 잠급니다.
		playersMutex.Lock()
		// 이미 가입한 기기인지 장부에서 찾아봅니다.
		record, exists := devicePlayers[req.DeviceId]
		// 처음 보는 기기라면
		if !exists {
			// 새 플레이어 번호를 배정한 기록을 만듭니다.
			record = &PlayerRecord{PlayerID: nextPlayerID}
			devicePlayers[req.DeviceId] = record // 기기ID 장부에 기록합니다.
			nextPlayerID++                       // 다음 사람을 위해 번호를 1 올립니다.
		}
		// 이전에 발급한 토큰이 남아 있다면
		if record.SessionToken != "" {
			delete(tokenOwners, record.SessionToken) // 옛 팔찌는 무효로 만듭니다. (재로그인 시 항상 새 팔찌 사용)
		}
		record.SessionToken = token                 // 유저 기록에 새 토큰을 적습니다.
		record.ExpiresAt = time.Now().Add(tokenTTL) // [보안] 만료 시각을 "지금 + 유효 시간"으로 정합니다.
		tokenOwners[token] = record                 // 팔찌 확인용 장부에도 "이 토큰은 이 기록의 주인 것"이라고 적습니다.
		playerID := record.PlayerID                 // 열쇠를 푼 뒤에도 쓸 수 있게 번호를 따로 복사해 둡니다.
		// 장부 수정이 끝났으니 열쇠를 풉니다.
		playersMutex.Unlock()

		res.Success = true       // 성공으로 표시합니다.
		res.PlayerId = playerID  // 배정된 플레이어 번호를 담습니다.
		res.SessionToken = token // 발급한 세션 토큰을 담습니다.
		res.Message = "로그인 성공"  // 안내 문구를 적습니다.
		log.Printf("로그인: 기기=%s, 플레이어번호=%d, 토큰 유효시간=%v", req.DeviceId, playerID, tokenTTL) // 서버 화면에 기록을 남깁니다. (토큰은 보안상 기록하지 않습니다)
	}

	// 응답 양식을 전송 가능한 데이터로 "포장"합니다.
	out, err := proto.Marshal(res)
	// 포장에 실패했다면
	if err != nil {
		// 서버 내부 오류라고 알려 줍니다.
		http.Error(w, "응답 생성 실패", http.StatusInternalServerError)
		return // 함수를 끝냅니다.
	}
	// "이 데이터는 프로토콜 버퍼입니다"라고 표시합니다.
	w.Header().Set("Content-Type", "application/x-protobuf")
	// 포장한 응답을 유니티로 보냅니다.
	w.Write(out)
}

// 실시간 이동 동기화(WebSocket)를 처리하는 함수입니다. 유니티가 /ws 주소로 접속합니다.
func wsHandler(w http.ResponseWriter, r *http.Request) {
	// [보안] 요청에서 토큰을 꺼냅니다. (헤더 우선, 헤더가 없을 때만 주소의 ?token= 보조)
	token := extractToken(r)
	// 토큰이 아예 없다면
	if token == "" {
		// "인증 필요"(401)라고 알리고 즉시 차단합니다. 유니티는 이 401을 보고 재로그인합니다.
		http.Error(w, "세션 토큰이 필요합니다", http.StatusUnauthorized)
		log.Println("웹소켓 차단: 토큰 없음", r.RemoteAddr) // 차단 기록을 남깁니다.
		return                                       // 함수를 끝냅니다.
	}

	// [보안] 토큰이 장부에 있고 만료되지 않았는지 확인합니다.
	playerID, valid := validateToken(token)
	// 장부에 없거나 만료된 토큰이라면
	if !valid {
		// "인증 실패"(401)라고 알리고 즉시 차단합니다. 유니티는 이 401을 보고 새 토큰을 받으러 갑니다.
		http.Error(w, "유효하지 않거나 만료된 세션 토큰입니다", http.StatusUnauthorized)
		log.Println("웹소켓 차단: 잘못되었거나 만료된 토큰", r.RemoteAddr) // 차단 기록을 남깁니다.
		return                                                  // 함수를 끝냅니다.
	}

	// 검증을 통과했으니 일반 HTTP 연결을 WebSocket 연결로 바꿉니다.
	conn, err := upgrader.Upgrade(w, r, nil)
	// 바꾸기에 실패했다면
	if err != nil {
		log.Println("WebSocket 연결 실패:", err) // 이유를 서버 화면에 기록합니다.
		return                                  // 함수를 끝냅니다.
	}

	// 받을 수 있는 패킷 최대 크기를 제한합니다. 너무 큰 패킷을 보내면 연결이 끊깁니다.
	conn.SetReadLimit(maxPacketSize)
	// "pongWait 안에 아무 소식이 없으면 죽은 연결"이라는 기한을 정합니다.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	// 상대가 확인 신호(Ping)에 응답(Pong)하면 기한을 새로 연장합니다.
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // 기한을 연장합니다.
		return nil                                     // 문제 없음을 알립니다.
	})

	// 이 접속자의 정보를 만듭니다. (플레이어 번호는 토큰으로 확인된 진짜 번호입니다)
	client := newClient(conn, playerID)
	// 이 접속자 전용 배달 고루틴을 시작합니다.
	go client.writePump()

	// 현재 접속자 명단을 고치는 동안 열쇠를 잠급니다.
	activeMutex.Lock()
	// 같은 플레이어가 이미 연결 중이라면
	if old, exists := activeClients[playerID]; exists {
		old.Close() // 이전 연결을 끊습니다. (한 계정이 두 자리를 차지하지 못하게 함)
		log.Printf("플레이어 %d 의 이전 연결을 종료합니다 (중복 접속).", playerID) // 기록을 남깁니다.
	}
	activeClients[playerID] = client // 새 연결을 명단에 등록합니다.
	activeMutex.Unlock()             // 열쇠를 풉니다.

	// 방에 배정합니다. 재접속이면 원래 방으로, 신규면 자리가 있는 방으로 들어갑니다.
	room, resumed := roomManager.Join(client)
	// 재접속으로 복귀했다면
	if resumed {
		log.Printf("플레이어 %d 재접속 복귀 -> 방 %d (현재 %d/%d명)", playerID, room.id, room.Count(), maxRoomPlayers) // 복귀 기록을 남깁니다.
	} else {
		log.Printf("플레이어 %d 입장 -> 방 %d (현재 %d/%d명)", playerID, room.id, room.Count(), maxRoomPlayers) // 신규 입장 기록을 남깁니다.
	}

	// 이 함수가 끝날 때(연결이 끊길 때) 자동으로 실행할 정리 작업입니다.
	defer func() {
		roomManager.Detach(room, client) // 방에서 바로 내보내지 않고 재접속 유예 타이머를 시작합니다. (이미 퇴장 처리됐다면 아무 일도 하지 않습니다)
		activeMutex.Lock()               // 현재 접속자 명단을 고치기 위해 열쇠를 잠급니다.
		// 명단에 적힌 연결이 "나 자신"일 때만 지웁니다. (새 연결로 교체된 경우를 보호)
		if activeClients[playerID] == client {
			delete(activeClients, playerID) // 명단에서 나를 지웁니다.
		}
		activeMutex.Unlock() // 열쇠를 풉니다.
		client.Close()       // 연결을 완전히 닫습니다.
		log.Printf("플레이어 %d 연결 종료", playerID) // 종료 기록을 남깁니다.
	}()

	// 연결이 끊길 때까지 계속 메시지를 기다리는 반복문입니다.
	for {
		// 유니티가 보낸 메시지를 하나 받습니다.
		msgType, data, err := conn.ReadMessage()
		// 받다가 오류가 나면(연결이 끊겼거나 기한 초과)
		if err != nil {
			// 유니티가 "정상 종료(코드 1000)"로 닫은 것이라면
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				roomManager.LeaveNow(room, client) // 유예 없이 즉시 퇴장시킵니다.
			}
			return // 반복을 끝내고 함수를 종료합니다. (위의 정리 작업이 실행됩니다)
		}
		// 메시지가 왔다는 것은 살아 있다는 뜻이므로 기한을 연장합니다.
		conn.SetReadDeadline(time.Now().Add(pongWait))
		// 바이너리(패킷) 형태가 아닌 메시지는 무시합니다.
		if msgType != websocket.BinaryMessage {
			continue // 다음 메시지를 기다립니다.
		}

		// 받은 데이터를 담을 빈 이동 동기화 양식을 준비합니다.
		move := &MoveSync{}
		// 받은 데이터를 이동 동기화 양식으로 개봉합니다.
		if err := proto.Unmarshal(data, move); err != nil {
			log.Println("이동 패킷 형식 오류:", err) // 오류를 기록합니다.
			continue                            // 이 메시지는 버리고 다음 메시지를 기다립니다.
		}

		// 패킷 안의 좌표를 꺼냅니다.
		pos := move.GetPosition()
		// 좌표 자체가 없는 패킷이라면
		if pos == nil {
			continue // 무시합니다.
		}
		x, y, z := pos.GetX(), pos.GetY(), pos.GetZ() // X, Y, Z 값을 꺼냅니다.
		// 하나라도 비정상 값이라면
		if !isValidCoord(x) || !isValidCoord(y) || !isValidCoord(z) {
			log.Printf("플레이어 %d 의 비정상 좌표를 버립니다.", playerID) // 기록을 남깁니다.
			continue                                              // 이 메시지는 버립니다.
		}

		// 방의 칠판에 쓰기 전에 안티치트 검증을 거칩니다. (패킷의 PlayerId 는 믿지 않고 토큰으로 확인된 접속자로 검증합니다)
		kick := room.UpdatePosition(client, x, y, z)
		// 상습 위반자라서 내보내야 한다면
		if kick {
			log.Printf("[안티치트] 플레이어 %d 를 반복 위반으로 퇴장시킵니다.", playerID) // 기록을 남깁니다.
			roomManager.LeaveNow(room, client)                                 // 유예 없이 즉시 방에서 지웁니다.
			return                                                             // 함수를 끝냅니다. (위의 정리 작업이 연결을 닫습니다)
		}
	}
}

// 프로그램이 시작되면 가장 먼저 실행되는 함수입니다.
func main() {
	http.HandleFunc("/", homeHandler)       // 기본 주소(/)로 오면 안내 화면을 보여 줍니다.
	http.HandleFunc("/login", loginHandler) // /login 주소로 오면 로그인 처리 함수를 실행합니다.
	http.HandleFunc("/ws", wsHandler)       // /ws 주소로 오면 실시간 처리 함수를 실행합니다.

	go tokenJanitor() // 만료된 토큰을 주기적으로 청소하는 고루틴을 시작합니다.

	// 서버가 접속을 기다릴 주소를 정합니다. 환경변수 GAME_LISTEN_ADDR 이 있으면 그 값을 쓰고, 없으면 기본값을 씁니다.
	addr := os.Getenv("GAME_LISTEN_ADDR")
	// 환경변수가 비어 있다면
	if addr == "" {
		addr = defaultListenAddr // 기본 주소를 씁니다.
	}

	log.Printf("서버 시작: %s 에서 접속을 기다립니다... (토큰 유효시간 %v, 쿼리 토큰 허용=%v)", addr, tokenTTL, allowQueryToken) // 시작 안내를 남깁니다.
	// 주소를 열고 접속을 기다립니다. 문제가 생기면 그 이유를 기록하고 종료합니다. (암호화는 앞단의 Caddy 가 담당합니다)
	log.Fatal(http.ListenAndServe(addr, nil))
}