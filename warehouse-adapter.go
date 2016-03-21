/*
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
*/

package main

import (
	"encoding/base64"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"github.com/golang/protobuf/proto"
	"github.com/openblockchain/obc-peer/events/consumer"
	_ "github.com/go-sql-driver/mysql"
	ehpb "github.com/openblockchain/obc-peer/protos"
	ehnot "evtprotos"
)

type Adapter struct {
	sync.RWMutex
	notfy chan *ehpb.OpenchainEvent
	db *sql.DB
	blockIns *sql.Stmt
	transIns *sql.Stmt
	clients map[string]ehnot.EventNotify_RegisterForNotificationsServer
}

func (a *Adapter) GetInterestedEvents() ([]*ehpb.Interest, error) {
	return []*ehpb.Interest{&ehpb.Interest{"block", ehpb.Interest_PROTOBUF}}, nil
	//return [] *ehpb.Interest{ &ehpb.InterestedEvent{"block", ehpb.Interest_JSON }}, nil
}

func (a *Adapter) Recv(msg *ehpb.OpenchainEvent) (bool, error) {
	a.Lock()
	a.notfy <- msg
	a.Unlock()
	return true, nil
}

func (a *Adapter) Disconnected(err error) {
	if err != nil {
		fmt.Printf("Error: %s\n", err)
	}
}

func (a *Adapter) PrintBlock(block *ehpb.Block) {
        blockTransactions := block.GetTransactions()
        for _, transaction := range blockTransactions {
               fmt.Printf("transaction [%s]\n", transaction.Type.String())
        }
 
}

func (a *Adapter) AddTransactions(blockid string, block *ehpb.Block) {
	blockTransactions := block.GetTransactions()
	for _, transaction := range blockTransactions {
		bytes, err := proto.Marshal(transaction)
		if err != nil {
			fmt.Sprintf("Error marshalling transaction : %s", err)
			return
		}
		_, err = a.transIns.Exec(transaction.Uuid, blockid, bytes)
		if err != nil {
			fmt.Printf("Error adding transaction %s\n", err)
		} else {
			fmt.Printf("Added transaction\n")
			a.SendNotifications(blockid, transaction)
		}
	}
}
func (a *Adapter) AddBlock(msg *ehpb.OpenchainEvent) {
	fmt.Printf("Adding block\n")
	switch x := msg.Event.(type) {
	case *ehpb.OpenchainEvent_Block:
		block := msg.GetBlock()
		bytes, err := proto.Marshal(block)
		if err != nil {
			fmt.Sprintf("Error marshalling block : %s", err)
			return
		}
		encoded := base64.StdEncoding.EncodeToString(block.PreviousBlockHash)
		 _, err = a.blockIns.Exec(encoded, bytes)
		if err != nil {
			fmt.Printf("Error adding block %s\n", err)
		} else {
			fmt.Printf("Added block\n")
			a.AddTransactions(encoded, block)
		}
	case *ehpb.OpenchainEvent_Generic:
	case nil:
		// The field is not set.
		fmt.Printf("event not set\n")
	default:
		fmt.Printf("unexpected type %T\n", x)
	}
}

func (a *Adapter) ReceiveMessages() {
	fmt.Printf("Waiting for blocks\n")
	for {
		select {
		case msg := <-a.notfy:
			a.AddBlock(msg)
		case <-time.After(60 * time.Second):
			fmt.Printf("Waiting for blocks\n")
		}
	}
}

// Chat implementation of the the Chat bidi streaming RPC function
func (a *Adapter) RegisterForNotifications(stream ehnot.EventNotify_RegisterForNotificationsServer) error {
	for {
		//new client
		client, err := stream.Recv()
		if err == io.EOF {
			fmt.Printf("Received EOF, ending Notifications\n")
			return nil
		}
		if err != nil {
			fmt.Printf("Error during notifications: %s\n", err)
			continue
		}
		fmt.Printf("Client : %s\n", client.Name)
		if a.clients[client.Name] != nil {
			fmt.Printf("Client %s already registered..ignoring\n", client.Name)
		} else {
			a.clients[client.Name] = stream
		}
	}
}

// Chat implementation of the the Chat bidi streaming RPC function
func (a *Adapter) SendNotifications(bid string, tx *ehpb.Transaction) {
	n := &ehnot.Notification{ Tid: tx.Uuid, Blockid: bid }
	for clname := range a.clients {
		clstream := a.clients[clname]
		clstream.Send(n)
	}
}
func (a *Adapter) EventNotificationServer(listenAddr string) {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		panic(fmt.Errorf("failed to listen: %v", err))
	}

	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	ehnot.RegisterEventNotifyServer(grpcServer, a)

        //start the event hub server
        go func() {
		fmt.Printf("Event Server Started on %s\n", listenAddr)
		err := grpcServer.Serve(lis)
		fmt.Printf("Event Server exited %s\n", err)
	}()	
}

func main() {
	var peerAddress string
	flag.StringVar(&peerAddress, "--peer", "127.0.0.1:31315", "peer address")

	var mysqlstr string
	flag.StringVar(&mysqlstr, "--mysqlstr", "root:password@tcp(127.0.0.1:3306)", "mysql connection string")

	mysqlstr = mysqlstr+"/blocks"

	var notserveraddr string
	flag.StringVar(&notserveraddr, "--notfylistener", "127.0.0.1:9999", "mysql connection string")

	fmt.Printf("Peer Address:<%s>, MySQL string:<%s>, BCSAPI Listener:<%s>\n", peerAddress, mysqlstr, notserveraddr)

	//we override the peerAddress set in chaincode_support.go
	done := make(chan *ehpb.OpenchainEvent)
	adapter := &Adapter{notfy: done, clients: make(map[string]ehnot.EventNotify_RegisterForNotificationsServer)}
	db, err := sql.Open("mysql", mysqlstr)
	adapter.db = db
	if err != nil {
		panic(err.Error())
	}
	adapter.blockIns, err = db.Prepare("INSERT INTO block VALUES( ? , ? )")
	if err != nil {
		panic(err.Error())
	}
	adapter.transIns, err = db.Prepare("INSERT INTO transaction VALUES( ?, ?, ?)")
	if err != nil {
		panic(err.Error())
	}
	obcEHClient := consumer.NewOpenchainEventsClient(peerAddress, adapter)
	if err = obcEHClient.Start(); err != nil {
		fmt.Printf("could not start chat %s\n", err)
		obcEHClient.Stop()
		return
	}

	adapter.EventNotificationServer(notserveraddr)

	adapter.ReceiveMessages()
}
