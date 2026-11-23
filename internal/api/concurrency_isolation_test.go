package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test変化の削除と表示中の局面の選択が競合しても削除済み局面を残さない(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	for range 50 {
		assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
		variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
		assertStatus(t, variation, http.StatusOK)
		if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		nodeID := state.Game.CurrentNodeID
		deletionBody := expectedNodeIDsBody(t, nodeID)
		start := make(chan struct{})
		selected := make(chan *httptest.ResponseRecorder, 1)
		deleted := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			<-start
			selected <- performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+nodeID+`"}`)
		}()
		go func() {
			<-start
			deleted <- performJSON(t, handler, http.MethodDelete, path+"/nodes/"+nodeID, deletionBody)
		}()
		close(start)
		selectionResponse := <-selected
		if selectionResponse.Code != http.StatusNoContent && selectionResponse.Code != http.StatusNotFound {
			t.Fatalf("削除と競合した表示中の局面選択の状態コード = %d、本文 = %s", selectionResponse.Code, selectionResponse.Body.String())
		}
		assertStatus(t, <-deleted, http.StatusOK)
		contextResponse := performJSON(t, handler, http.MethodGet, "/api/agent/context", ``)
		if contextResponse.Code == http.StatusConflict {
			continue
		}
		assertStatus(t, contextResponse, http.StatusOK)
		var contextState agentContext
		if err := json.Unmarshal(contextResponse.Body.Bytes(), &contextState); err != nil {
			t.Fatal(err)
		}
		if contextState.NodeID != "root" {
			t.Fatalf("削除後の表示中の局面 = %q、期待値 = 開始局面または未選択", contextState.NodeID)
		}
	}
}

func Test表示元の削除が先に完了した局面移動は古い表示中の局面を使わない(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	type completedRequest struct {
		kind     string
		response *httptest.ResponseRecorder
	}
	for range 50 {
		variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
		assertStatus(t, variation, http.StatusOK)
		if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		nodeID := state.Game.CurrentNodeID
		assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+nodeID+`"}`), http.StatusNoContent)

		start := make(chan struct{})
		completed := make(chan completedRequest, 2)
		go func() {
			<-start
			completed <- completedRequest{kind: "移動", response: performJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+state.Game.ID+`:`+nodeID+`","nodeId":"root"}`)}
		}()
		go func() {
			<-start
			completed <- completedRequest{kind: "削除", response: performJSON(t, handler, http.MethodDelete, path+"/nodes/"+nodeID, expectedNodeIDsBody(t, nodeID))}
		}()
		close(start)
		first, second := <-completed, <-completed
		deletion, navigation := first, second
		if first.kind == "移動" {
			deletion, navigation = second, first
		}
		assertStatus(t, deletion.response, http.StatusOK)
		if navigation.response.Code != http.StatusOK && navigation.response.Code != http.StatusNotFound {
			t.Fatalf("削除と競合した局面移動の状態コード = %d、本文 = %s", navigation.response.Code, navigation.response.Body.String())
		}
		if first.kind == "削除" && navigation.response.Code != http.StatusNotFound {
			t.Fatalf("削除完了後に古い表示中の局面から移動できた：状態コード = %d、本文 = %s", navigation.response.Code, navigation.response.Body.String())
		}
	}
}

func Test解析情報を保存した後も同じ局面を共有できる(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?nodeId=root&visits=100", ``), http.StatusOK)
	selection := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`)
	assertStatus(t, selection, http.StatusNoContent)
}

func Test対局の読み取りと同時でも表示中の局面を選択できる(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerDone := make(chan int, 1)
	go func() {
		for ctx.Err() == nil {
			for _, path := range []string{"/api/games", "/api/games/" + state.Game.ID} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if recorder.Code != http.StatusOK {
					readerDone <- recorder.Code
					return
				}
			}
		}
		readerDone <- http.StatusOK
	}()
	for range 200 {
		response := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`)
		assertStatus(t, response, http.StatusNoContent)
	}
	cancel()
	if status := <-readerDone; status != http.StatusOK {
		t.Fatalf("対局読取状態コード = %d", status)
	}
}
