package taskservice

import (
	"context"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

func RerunTask(ctx context.Context, sourceJobId string) (models.Task, error) {
	var task models.Task

	err := database.DB.WithContext(ctx).Where("job_id = ? AND status IN ?", sourceJobId, []string{"Completed", "Failed"}).First(&task).Error

	if err != nil {
		return models.Task{}, err
	}

	rerunTask := models.Task{
		TaskName:    task.TaskName,
		TaskType:    task.TaskType,
		Payload:     task.Payload,
		SourceRunId: sourceJobId,
	}

	result_reply := CreateTask_Direct(ctx, rerunTask)

	return result_reply, nil
}
