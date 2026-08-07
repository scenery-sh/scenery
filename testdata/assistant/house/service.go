package house

import (
	"context"

	housecontract "example.test/assistantfixture/house/scenerycontract"
	scenery "scenery.sh"
)

type Service struct{}

func NewService(context.Context, housecontract.HouseConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) ProcessScene(_ context.Context, input housecontract.ProcessSceneInput) (housecontract.ProcessSceneOutcome, error) {
	if input.SceneId == "declared-error" {
		return housecontract.ProcessSceneInvalidScene{Problem: scenery.Problem{Code: "invalid_scene", Message: "scene is invalid"}}, nil
	}
	return housecontract.ProcessSceneProcessed{Value: housecontract.ProcessSceneResult{Status: "processed:" + input.SceneId}}, nil
}
