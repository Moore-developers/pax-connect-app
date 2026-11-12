package controller

import (
	"sort"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

// addTrafficExit accumulates a real exit before pagination or sample limits.
func addTrafficExit(row *pb.TrafficRow, id, name, direction string, up, down, connections uint64) {
	for _, exit := range row.Exits {
		if exit.RouteId == id && exit.Direction == direction {
			exit.Upload += up
			exit.Download += down
			exit.Connections += connections
			return
		}
	}
	row.Exits = append(row.Exits, &pb.TrafficExit{RouteId: id, RouteName: name, Direction: direction, Upload: up, Download: down, Connections: connections})
}

func finishTrafficExits(row *pb.TrafficRow) {
	sort.Slice(row.Exits, func(i, j int) bool {
		a, b := row.Exits[i], row.Exits[j]
		if a.Upload+a.Download != b.Upload+b.Download {
			return a.Upload+a.Download > b.Upload+b.Download
		}
		if a.RouteId != b.RouteId {
			return a.RouteId < b.RouteId
		}
		return a.Direction < b.Direction
	})
	if len(row.Exits) == 1 {
		exit := row.Exits[0]
		row.RouteId, row.RouteName, row.Direction = exit.RouteId, exit.RouteName, exit.Direction
	} else if len(row.Exits) > 1 {
		row.RouteId, row.RouteName, row.Direction = "", "", "mixed"
	}
}

func sortTrafficRows(rows []*pb.TrafficRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.LastActiveAt != b.LastActiveAt {
			return a.LastActiveAt > b.LastActiveAt
		}
		if a.Upload+a.Download != b.Upload+b.Download {
			return a.Upload+a.Download > b.Upload+b.Download
		}
		return a.Id < b.Id
	})
}

func summarizesTrafficDimension(dimension string) bool {
	return dimension == "application" || dimension == "system" || dimension == "domain" || dimension == ""
}
