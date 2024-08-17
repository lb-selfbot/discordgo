package protos

//go:generate protoc --go_out=. --go_opt=Mprotos/PreloadedUserSettings.proto=./protos --go_opt=Mprotos/FrecencyUserSettings.proto=./protos ./protos/PreloadedUserSettings.proto ./protos/FrecencyUserSettings.proto
