package handlers

type PlayerInterface interface {
	Play(fileName string) error
}