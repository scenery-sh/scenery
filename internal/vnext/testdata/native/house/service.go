package house

import (
	"context"

	housecontract "example.test/nativeapp/house/scenerycontract"
)

//scenery:service
type Service struct{}

func NewService(context.Context, housecontract.HouseConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (service *Service) ProcessScene(_ context.Context, input housecontract.ProcessSceneInput) (housecontract.ProcessSceneOutcome, error) {
	return housecontract.ProcessSceneProcessed{Value: housecontract.ProcessSceneResult{Status: "processed:" + input.SceneId}}, nil
}
