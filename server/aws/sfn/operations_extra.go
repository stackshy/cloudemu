package sfn

import (
	"context"
	"net/http"
)

func (h *Handler) publishStateMachineVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *publishVersionRequest) (any, error) {
		versionArn, created, err := h.sfn.PublishStateMachineVersion(ctx, req.StateMachineArn, req.Description)
		if err != nil {
			return nil, err
		}

		return publishVersionResponse{StateMachineVersionArn: versionArn, CreationDate: epoch(created)}, nil
	})
}

//nolint:dupl // versions and aliases list into distinct wire item/response types; merging them would obscure both
func (h *Handler) listStateMachineVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *stateMachineArnRequest) (any, error) {
		versions, err := h.sfn.ListStateMachineVersions(ctx, req.StateMachineArn)
		if err != nil {
			return nil, err
		}

		items := make([]versionListItem, 0, len(versions))

		for i := range versions {
			v := &versions[i]
			items = append(items, versionListItem{StateMachineVersionArn: v.ARN, CreationDate: epoch(v.CreationDate)})
		}

		return listVersionsResponse{StateMachineVersions: items}, nil
	})
}

func (h *Handler) deleteStateMachineVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *versionArnRequest) (any, error) {
		if err := h.sfn.DeleteStateMachineVersion(ctx, req.StateMachineVersionArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) createStateMachineAlias(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createAliasRequest) (any, error) {
		arn, created, err := h.sfn.CreateStateMachineAlias(
			ctx, req.Name, req.Description, routingFromWire(req.RoutingConfiguration))
		if err != nil {
			return nil, err
		}

		return createAliasResponse{StateMachineAliasArn: arn, CreationDate: epoch(created)}, nil
	})
}

func (h *Handler) describeStateMachineAlias(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aliasArnRequest) (any, error) {
		alias, err := h.sfn.DescribeStateMachineAlias(ctx, req.StateMachineAliasArn)
		if err != nil {
			return nil, err
		}

		return describeAliasResponse{
			StateMachineAliasArn: alias.ARN, Name: alias.Name, Description: alias.Description,
			RoutingConfiguration: routingToWire(alias.Routing),
			CreationDate:         epoch(alias.CreationDate), UpdateDate: epoch(alias.UpdateDate),
		}, nil
	})
}

func (h *Handler) updateStateMachineAlias(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateAliasRequest) (any, error) {
		updated, err := h.sfn.UpdateStateMachineAlias(
			ctx, req.StateMachineAliasArn, req.Description, routingFromWire(req.RoutingConfiguration))
		if err != nil {
			return nil, err
		}

		return updateAliasResponse{UpdateDate: epoch(updated)}, nil
	})
}

func (h *Handler) deleteStateMachineAlias(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aliasArnRequest) (any, error) {
		if err := h.sfn.DeleteStateMachineAlias(ctx, req.StateMachineAliasArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

//nolint:dupl // versions and aliases list into distinct wire item/response types; merging them would obscure both
func (h *Handler) listStateMachineAliases(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *stateMachineArnRequest) (any, error) {
		aliases, err := h.sfn.ListStateMachineAliases(ctx, req.StateMachineArn)
		if err != nil {
			return nil, err
		}

		items := make([]aliasListItem, 0, len(aliases))

		for i := range aliases {
			a := &aliases[i]
			items = append(items, aliasListItem{StateMachineAliasArn: a.ARN, CreationDate: epoch(a.CreationDate)})
		}

		return listAliasesResponse{StateMachineAliases: items}, nil
	})
}

func (h *Handler) createActivity(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createActivityRequest) (any, error) {
		arn, created, err := h.sfn.CreateActivity(ctx, req.Name, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return createActivityResponse{ActivityArn: arn, CreationDate: epoch(created)}, nil
	})
}

func (h *Handler) describeActivity(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *activityArnRequest) (any, error) {
		act, err := h.sfn.DescribeActivity(ctx, req.ActivityArn)
		if err != nil {
			return nil, err
		}

		return describeActivityResponse{ActivityArn: act.ARN, Name: act.Name, CreationDate: epoch(act.CreationDate)}, nil
	})
}

func (h *Handler) deleteActivity(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *activityArnRequest) (any, error) {
		if err := h.sfn.DeleteActivity(ctx, req.ActivityArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listActivities(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listActivitiesRequest) (any, error) {
		activities, err := h.sfn.ListActivities(ctx)
		if err != nil {
			return nil, err
		}

		start, end, next := pageWindow(len(activities), req.NextToken, req.MaxResults)
		activities = activities[start:end]

		items := make([]activityListItem, 0, len(activities))

		for i := range activities {
			a := &activities[i]
			items = append(items, activityListItem{ActivityArn: a.ARN, Name: a.Name, CreationDate: epoch(a.CreationDate)})
		}

		return listActivitiesResponse{Activities: items, NextToken: next}, nil
	})
}

func (h *Handler) getActivityTask(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getActivityTaskRequest) (any, error) {
		token, input, err := h.sfn.GetActivityTask(ctx, req.ActivityArn, req.WorkerName)
		if err != nil {
			return nil, err
		}

		return getActivityTaskResponse{TaskToken: token, Input: input}, nil
	})
}

func (h *Handler) sendTaskSuccess(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *sendTaskSuccessRequest) (any, error) {
		if err := h.sfn.SendTaskSuccess(ctx, req.TaskToken, req.Output); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) sendTaskFailure(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *sendTaskFailureRequest) (any, error) {
		if err := h.sfn.SendTaskFailure(ctx, req.TaskToken, req.Error, req.Cause); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) sendTaskHeartbeat(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *sendTaskHeartbeatRequest) (any, error) {
		if err := h.sfn.SendTaskHeartbeat(ctx, req.TaskToken); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.sfn.TagResource(ctx, req.ResourceArn, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.sfn.UntagResource(ctx, req.ResourceArn, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceArnRequest) (any, error) {
		tags, err := h.sfn.ListTagsForResource(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return listTagsResponse{Tags: mapToTags(tags)}, nil
	})
}
