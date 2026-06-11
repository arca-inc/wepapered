package main

import (
	"fmt"
	"log"
	"os/exec"

	"github.com/gorilla/websocket"
)

func (s *WSServer) handleInstall(conn *websocket.Conn, msg WEMessage) {
	if msg.Method == "subscribe" && len(msg.Args) > 0 {
		if id, ok := msg.Args[0].(string); ok {
			log.Printf("[WE] subscribe called for %s - opening in Steam", id)
			exec.Command("xdg-open", "steam://url/CommunityFilePage/"+id).Start()
		} else if idFloat, ok := msg.Args[0].(float64); ok {
			idStr := fmt.Sprintf("%d", int(idFloat))
			log.Printf("[WE] subscribe called for %s - opening in Steam", idStr)
			exec.Command("xdg-open", "steam://url/CommunityFilePage/"+idStr).Start()
		}
	}
}
