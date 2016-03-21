package main

import (
	"flag"
	"fmt"
	"io"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	enotfy "evtprotos"
)

func processEvents(client enotfy.EventNotifyClient, name string) error {
	stream, err := client.RegisterForNotifications(context.Background())
	if err = stream.Send(&enotfy.Client{name}); err != nil {
		fmt.Printf("error on Register send %s\n", err)
		return err
	}

	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("Received %s, %s\n", in.Tid, in.Blockid)
	}
}

var (
        serverAddr         = flag.String("server_addr", "127.0.0.1:9999", "The server address in the format of host:port")
        clientName         = flag.String("client_name", "client", "The server address in the format of host:port")
)

func main() {
        flag.Parse()
        var opts []grpc.DialOption
        opts = append(opts, grpc.WithInsecure())
        conn, err := grpc.Dial(*serverAddr, opts...)
        if err != nil {
                fmt.Printf("fail to dial: %v", err)
		return
        }
        defer conn.Close()
        client := enotfy.NewEventNotifyClient(conn)
	processEvents(client, *clientName)

}
