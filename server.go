package main

import (
    "log"
    "math/rand"
    "net/http"
    "strconv"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

type Room struct {
    Code          string
    Clients       map[*websocket.Conn]string 
    CurrentDrawer *websocket.Conn            
    WordToDraw    string                     
}

type Message struct {
    Type     string `json:"type"`
    Message  string `json:"message,omitempty"`
    Nickname string `json:"nickname,omitempty"`
    RoomCode string `json:"roomCode,omitempty"`
    X        int    `json:"x,omitempty"`
    Y        int    `json:"y,omitempty"`
    PrevX    int    `json:"prevX,omitempty"`
    PrevY    int    `json:"prevY,omitempty"`
}

var rooms = make(map[string]*Room)
var mutex = &sync.Mutex{}
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true
    },
}

var wordList = []string{"maison", "chat", "chien", "arbre", "voiture", "soleil", "ordinateur", "livre"}

func handleConnection(w http.ResponseWriter, r *http.Request) {
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("Erreur lors de l'upgrade WebSocket:", err)
        return
    }
    defer func() {
        log.Println("Connexion fermée")
        ws.Close()
    }()

    log.Println("Nouvelle connexion WebSocket établie")
    var nickname string = "Anonyme"
    var roomCode string
    var msg Message

    for {
        err := ws.ReadJSON(&msg)
        if err != nil {
            log.Println("Erreur de réception:", err)
            break
        }

        log.Printf("Message reçu: %v\n", msg)

        switch msg.Type {
        case "nickname":
            nickname = msg.Nickname
            log.Println("Pseudo mis à jour: ", nickname)
            if roomCode != "" {
                mutex.Lock()
                rooms[roomCode].Clients[ws] = nickname
                mutex.Unlock()
            }

        case "createRoom":
            roomCode = generateRoomCode()
            mutex.Lock()
            rooms[roomCode] = &Room{
                Code:    roomCode,
                Clients: make(map[*websocket.Conn]string),
            }
            rooms[roomCode].Clients[ws] = nickname
            mutex.Unlock()
            log.Println("Room créée avec le code :", roomCode)
            ws.WriteJSON(Message{Type: "roomCreated", RoomCode: roomCode})

        case "joinRoom":
            mutex.Lock()
            room, exists := rooms[msg.RoomCode]
            if exists {
                roomCode = msg.RoomCode
                room.Clients[ws] = nickname
                mutex.Unlock()
                ws.WriteJSON(Message{Type: "roomJoined", RoomCode: msg.RoomCode})

                if len(room.Clients) >= 2 && room.CurrentDrawer == nil {
                    startNewRound(room, nil)
                }
            } else {
                mutex.Unlock()
                ws.WriteJSON(Message{Type: "error", Message: "Room not found"})
            }

        case "message":
            msg.Nickname = nickname
            broadcastMessage(msg, msg.RoomCode)

        case "draw":
            handleDraw(ws, msg)

        case "guess":
            handleGuess(ws, msg)
        }
    }
}

func handleDraw(ws *websocket.Conn, msg Message) {
    mutex.Lock()
    room, exists := rooms[msg.RoomCode]
    mutex.Unlock()
    if exists {
        if ws != room.CurrentDrawer {
            // Ignorer les messages de dessin provenant de non-dessinateurs
            return
        }
        broadcastDraw(msg, msg.RoomCode, ws)
    }
}

func handleGuess(ws *websocket.Conn, msg Message) {
    mutex.Lock()
    room, exists := rooms[msg.RoomCode]
    mutex.Unlock()
    if exists {
        nickname := room.Clients[ws]
        if msg.Message == room.WordToDraw {
            // Supposition correcte
            broadcastMessage(Message{
                Type:    "guessCorrect",
                Message: nickname + " a deviné le mot !",
            }, msg.RoomCode)

            ws.WriteJSON(Message{
                Type:    "youWon",
                Message: "Vous avez gagné un point, c'est votre tour de dessiner !",
            })

            broadcastMessage(Message{Type: "clearCanvas"}, msg.RoomCode)

            startNewRound(room, ws)
        } else {
            broadcastMessage(Message{
                Type:     "message",
                Nickname: nickname,
                Message:  msg.Message,
            }, msg.RoomCode)
        }
    }
}

func startNewRound(room *Room, nextDrawer *websocket.Conn) {
    mutex.Lock()
    defer mutex.Unlock()

    if nextDrawer != nil {
        room.CurrentDrawer = nextDrawer
    } else {
        if room.CurrentDrawer == nil {
            room.CurrentDrawer = getRandomPlayer(room.Clients)
        } else {
            room.CurrentDrawer = getNextPlayer(room.Clients, room.CurrentDrawer)
        }
    }

    word := wordList[rand.Intn(len(wordList))]
    room.WordToDraw = word

    if room.CurrentDrawer != nil {
        room.CurrentDrawer.WriteJSON(Message{
            Type:    "yourWord",
            Message: word,
        })
    }

    for client := range room.Clients {
        if client != room.CurrentDrawer {
            client.WriteJSON(Message{
                Type:    "startGuessing",
                Message: "Un joueur dessine, essayez de deviner le mot !",
            })
        }
    }
}

func getRandomPlayer(clients map[*websocket.Conn]string) *websocket.Conn {
    keys := make([]*websocket.Conn, 0, len(clients))
    for client := range clients {
        keys = append(keys, client)
    }
    if len(keys) == 0 {
        return nil
    }
    return keys[rand.Intn(len(keys))]
}

func getNextPlayer(clients map[*websocket.Conn]string, current *websocket.Conn) *websocket.Conn {
    keys := make([]*websocket.Conn, 0, len(clients))
    for client := range clients {
        keys = append(keys, client)
    }
    if len(keys) == 0 {
        return nil
    }
    var next *websocket.Conn
    for i, client := range keys {
        if client == current {
            next = keys[(i+1)%len(keys)]
            break
        }
    }
    if next == nil {
        next = keys[0]
    }
    return next
}

func broadcastMessage(msg Message, roomCode string) {
    mutex.Lock()
    defer mutex.Unlock()
    room, exists := rooms[roomCode]
    if exists {
        for client := range room.Clients {
            err := client.WriteJSON(msg)
            if err != nil {
                log.Println("Erreur d'envoi au client:", err)
                client.Close()
                delete(room.Clients, client)
            }
        }
    }
}

func broadcastDraw(msg Message, roomCode string, sender *websocket.Conn) {
    mutex.Lock()
    defer mutex.Unlock()
    room, exists := rooms[roomCode]
    if exists {
        for client := range room.Clients {
            if client != sender {
                err := client.WriteJSON(msg)
                if err != nil {
                    log.Println("Erreur d'envoi du dessin au client:", err)
                    client.Close()
                    delete(room.Clients, client)
                }
            }
        }
    }
}

func generateRoomCode() string {
    rand.Seed(time.Now().UnixNano())
    return strconv.Itoa(rand.Intn(10000)) 
}

func main() {
    http.HandleFunc("/ws", handleConnection)
    log.Println("Serveur en écoute sur :12345")
    err := http.ListenAndServe(":12345", nil)
    if err != nil {
        log.Fatal("Erreur du serveur:", err)
    }
}
