package controller

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"time"
)

func savedExitProto(v *config.SavedMeasurement) *pb.RouteExitSummary {
	return &pb.RouteExitSummary{At: timestamppb.New(v.At), Success: v.Success, TestedConfigRevision: v.ConfigRevision, ErrorCode: v.Error, ErrorParams: v.Params, Ip: v.ExitIP, Location: exitLocationProto(v.ExitLocation), LastLocation: exitLocationProto(v.LastExitLocation)}
}
func exitLocationValue(v *pb.ExitLocation) *config.ExitLocation {
	if v == nil {
		return nil
	}
	result := &config.ExitLocation{IP: v.Ip, CountryCode: v.CountryCode, Country: v.Country, Region: v.Region, City: v.City, ISP: v.Isp, ASN: v.Asn, Source: v.Source, GeoSource: v.GeoSource, GeoError: v.GeoError, At: v.At.AsTime(), ConfigRevision: v.ConfigRevision, Network: v.Network}
	if v.GeoAt != nil {
		result.GeoAt = v.GeoAt.AsTime()
	}
	if v.At == nil {
		result.At = time.Time{}
	}
	return result
}
func savedExit(v *pb.RouteExitSummary) *config.SavedMeasurement {
	return &config.SavedMeasurement{At: v.At.AsTime(), Success: v.Success, ConfigRevision: v.TestedConfigRevision, Error: v.ErrorCode, Params: v.ErrorParams, ExitIP: v.Ip, ExitLocation: exitLocationValue(v.Location), LastExitLocation: exitLocationValue(v.LastLocation)}
}
