package conversation

import (
	"github.com/gorilla/websocket"
	"html"
	"log"
	"minichat/constant"
	"strings"
	"sync"
)

type ConversationManager struct {
	Rooms          map[string]*Room
	Register       chan *Client
	unregister     chan *Client
	broadcast      chan Message
	registerLock   *sync.RWMutex
	unregisterLock *sync.RWMutex
	broadcastLock  *sync.RWMutex
}

type Message struct {
	RoomNumber string `json:"room_number"`
	UserName   string `json:"username"`
	Cmd        string `json:"cmd"`
	Payload    string `json:"payload"`
}

const MaxPayloadBytes = 4 * 1024 * 1024

type Client struct {
	RoomNumber string
	UserName   string
	Password   string
	Send       chan Message
	Conn       *websocket.Conn
	Stop       chan bool
}

type Room struct {
	Clients       map[*Client]*Client
	RoomName      string
	Password      string
	MessageOwners map[string]string
}

var Manager = ConversationManager{
	broadcast:      make(chan Message),
	Register:       make(chan *Client),
	unregister:     make(chan *Client),
	Rooms:          make(map[string]*Room),
	registerLock:   new(sync.RWMutex),
	unregisterLock: new(sync.RWMutex),
	broadcastLock:  new(sync.RWMutex),
}

func (manager *ConversationManager) Start() {
	for {
		select {
		case client := <-manager.Register:
			manager.registerLock.Lock()
			if _, ok := manager.Rooms[client.RoomNumber]; !ok {
				manager.Rooms[client.RoomNumber] = &Room{
					Clients:       make(map[*Client]*Client),
					Password:      client.Password,
					MessageOwners: make(map[string]string),
				}
			}
			manager.Rooms[client.RoomNumber].Clients[client] = client
			go func() {
				manager.broadcast <- Message{
					UserName:   client.UserName,
					Payload:    constant.JoinSuccess + constant.Online + formatOnlineNames(manager.Rooms[client.RoomNumber]),
					RoomNumber: client.RoomNumber,
					Cmd:        constant.CmdJoin,
				}
			}()
			manager.registerLock.Unlock()

		case client := <-manager.unregister:
			manager.unregisterLock.Lock()
			err := client.Conn.Close()
			if err != nil {
				return
			}
			if _, ok := manager.Rooms[client.RoomNumber]; ok {
				delete(manager.Rooms[client.RoomNumber].Clients, client)
				if len(manager.Rooms[client.RoomNumber].Clients) == 0 {
					delete(manager.Rooms, client.RoomNumber)
				}
				safeClose(client.Send)

				if manager.Rooms != nil && len(manager.Rooms) != 0 && manager.Rooms[client.RoomNumber] != nil && client.RoomNumber != "" {
					for c := range manager.Rooms[client.RoomNumber].Clients {
						c.Send <- Message{
							UserName:   client.UserName,
							Payload:    constant.ExitSuccess + constant.Online + formatOnlineNames(manager.Rooms[client.RoomNumber]),
							RoomNumber: client.RoomNumber,
							Cmd:        constant.CmdExit,
						}
					}
				}
			}
			manager.unregisterLock.Unlock()

		case message := <-manager.broadcast:
			manager.broadcastLock.RLock()
			if manager.Rooms != nil && len(manager.Rooms) != 0 && manager.Rooms[message.RoomNumber] != nil && message.RoomNumber != "" {
				for c := range manager.Rooms[message.RoomNumber].Clients {
					if c != nil && c.Conn != nil && c.Send != nil {
						c.Send <- message
					}
				}
			}
			manager.broadcastLock.RUnlock()
		}

	}
}

func (manager *ConversationManager) storeMessageOwner(roomNumber, messageID, userName string) {
	if roomNumber == "" || messageID == "" || userName == "" {
		return
	}
	manager.broadcastLock.Lock()
	defer manager.broadcastLock.Unlock()

	room := manager.Rooms[roomNumber]
	if room == nil {
		return
	}
	if room.MessageOwners == nil {
		room.MessageOwners = make(map[string]string)
	}
	room.MessageOwners[messageID] = userName
}

func (manager *ConversationManager) deleteOwnedMessage(roomNumber, messageID, userName string) bool {
	if roomNumber == "" || messageID == "" || userName == "" {
		return false
	}
	manager.broadcastLock.Lock()
	defer manager.broadcastLock.Unlock()

	room := manager.Rooms[roomNumber]
	if room == nil || room.MessageOwners == nil {
		return false
	}

	if owner, ok := room.MessageOwners[messageID]; !ok || owner != userName {
		return false
	}

	delete(room.MessageOwners, messageID)
	return true
}

func formatOnlineNames(room *Room) string {
	names := ""
	for key := range room.Clients {
		names += "[ " + html.EscapeString(key.UserName) + " ], "
	}
	return "<span class='is-inline-block'>" + strings.TrimSuffix(names, ", ") + "</span>"
}

func safeClose(ch chan Message) {
	defer func() {
		if recover() != nil {
			log.Println("ch is closed")
		}
	}()
	close(ch)
	log.Println("ch closed successfully")
}
