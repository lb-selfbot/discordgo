package protos

//go:generate protoc --go_out=. --go_opt=Mprotos/PreloadedUserSettings.proto=./protos protos/PreloadedUserSettings.proto