package collector

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/applicationautoscaling"
	"github.com/aws/aws-sdk-go/service/applicationautoscaling/applicationautoscalingiface"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"

	"github.com/FrankieFinancial/ecs-exporter/log"
	"github.com/FrankieFinancial/ecs-exporter/types"
)

const (
	maxServicesECSAPI = 10
)

// ECSGatherer is the interface that implements the methods required to gather ECS data
type ECSGatherer interface {
	GetClusters() ([]*types.ECSCluster, error)
	GetClusterServices(cluster *types.ECSCluster) ([]*types.ECSService, error)
	GetClusterScalableTargets(cluster *types.ECSCluster) ([]*types.ECSScalableTarget, error)
	GetClusterContainerInstances(cluster *types.ECSCluster) ([]*types.ECSContainerInstance, error)
}

// Generate ECS API mocks running go generate
//go:generate mockgen -source ../vendor/github.com/aws/aws-sdk-go/service/ecs/ecsiface/interface.go -package sdk -destination ../mock/aws/sdk/ecsiface_mock.go

// ECSClient is a wrapper for AWS ecs client that implements helpers to get ECS clusters metrics
type ECSClient struct {
	client           ecsiface.ECSAPI
	scaleClient      applicationautoscalingiface.ApplicationAutoScalingAPI
	ecsApiMaxResults int64
}

// NewECSClient will return an initialized ECSClient
func NewECSClient(awsRegion string, roleArn string) (*ECSClient, error) {
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(awsRegion),
	}))

	if sess == nil {
		return nil, fmt.Errorf("error creating aws session")
	}

	var (
		creds *credentials.Credentials
	)

	if roleArn == "" {
		log.Debugf("Using local cred chain")
		creds = nil
	}

	if roleArn != "" {
		log.Debugf("Assuming role arn")
		creds = stscreds.NewCredentials(sess, roleArn)
	}

	return &ECSClient{
		client:           ecs.New(sess, aws.NewConfig().WithCredentials(creds)),
		scaleClient:      applicationautoscaling.New(sess, aws.NewConfig().WithCredentials(creds)),
		ecsApiMaxResults: 100,
	}, nil
}

// GetClusters will get the clusters from the ECS API
func (e *ECSClient) GetClusters() ([]*types.ECSCluster, error) {
	cArns := []*string{}
	params := &ecs.ListClustersInput{
		MaxResults: aws.Int64(e.ecsApiMaxResults),
	}

	// Get cluster IDs
	log.Debugf("Getting cluster list for region")
	for {
		resp, err := e.client.ListClusters(params)
		if err != nil {
			return nil, err
		}

		for _, c := range resp.ClusterArns {
			cArns = append(cArns, c)
		}
		if resp.NextToken == nil || aws.StringValue(resp.NextToken) == "" {
			break
		}
		params.NextToken = resp.NextToken
	}

	// Get service descriptions
	// TODO: this has a 100 cluster limit, split calls in 100 by 100
	params2 := &ecs.DescribeClustersInput{
		Clusters: cArns,
	}
	resp2, err := e.client.DescribeClusters(params2)
	if err != nil {
		return nil, err
	}

	cs := []*types.ECSCluster{}
	log.Debugf("Getting cluster descriptions")
	for _, c := range resp2.Clusters {
		ec := &types.ECSCluster{
			ID:   aws.StringValue(c.ClusterArn),
			Name: aws.StringValue(c.ClusterName),
		}
		cs = append(cs, ec)
	}

	log.Debugf("Got %d clusters", len(cs))
	return cs, nil
}

// srvRes Internal	struct used to return error and result from goroutiens
type srvRes struct {
	result []*types.ECSService
	err    error
}

// GetClusterServices will return all the services from a cluster
func (e *ECSClient) GetClusterServices(cluster *types.ECSCluster) ([]*types.ECSService, error) {

	sArns := []*string{}

	// Get service ids
	params := &ecs.ListServicesInput{
		Cluster:    aws.String(cluster.ID),
		MaxResults: aws.Int64(e.ecsApiMaxResults),
	}

	log.Debugf("Getting service list for cluster: %s", cluster.Name)
	for {
		resp, err := e.client.ListServices(params)
		if err != nil {
			return nil, err
		}

		for _, s := range resp.ServiceArns {
			sArns = append(sArns, s)
		}

		if resp.NextToken == nil || aws.StringValue(resp.NextToken) == "" {
			break
		}
		params.NextToken = resp.NextToken
	}

	res := []*types.ECSService{}
	// If no services then nothing to fetch
	if len(sArns) == 0 {
		log.Debugf("Ignoring services fetching, no services in cluster: %s", cluster.Name)
		return res, nil
	}

	servC := make(chan srvRes)

	// Only can grab 10 services at a time, create calls in blocks of 10 services
	totalGr := 0 // counter for goroutines
	for i := 0; i <= len(sArns)/maxServicesECSAPI; i++ {
		st := i * maxServicesECSAPI
		// Check if the last call is neccesary (las call only made when the division remaider is present)
		if st >= len(sArns) {
			break
		}
		end := st + maxServicesECSAPI
		var spss []*string
		if end > len(sArns) {
			spss = sArns[st:]
		} else {
			spss = sArns[st:end]
		}

		totalGr++
		// Make a call on goroutine for each service blocks
		go func(services []*string) {
			log.Debugf("Getting service descriptions for cluster: %s", cluster.Name)
			params := &ecs.DescribeServicesInput{
				Services: services,
				Cluster:  aws.String(cluster.ID),
			}
			resp, err := e.client.DescribeServices(params)
			if err != nil {
				servC <- srvRes{nil, err}
			}

			ss := []*types.ECSService{}

			for _, s := range resp.Services {
				es := &types.ECSService{
					ID:       aws.StringValue(s.ServiceArn),
					Name:     aws.StringValue(s.ServiceName),
					DesiredT: aws.Int64Value(s.DesiredCount),
					RunningT: aws.Int64Value(s.RunningCount),
					PendingT: aws.Int64Value(s.PendingCount),
				}
				ss = append(ss, es)
			}

			servC <- srvRes{ss, nil}

		}(spss)

	}

	// Get all results
	for i := 0; i < totalGr; i++ {
		gRes := <-servC
		if gRes.err != nil {
			return res, gRes.err
		}
		res = append(res, gRes.result...)
	}

	log.Debugf("Got %d services on cluster %s", len(res), cluster.Name)
	return res, nil
}

// GetClusterScalableTargets will return all the scalable targets for the cluster
func (e *ECSClient) GetClusterScalableTargets(cluster *types.ECSCluster) ([]*types.ECSScalableTarget, error) {
	scalableTargets := []*types.ECSScalableTarget{}
	params := &applicationautoscaling.DescribeScalableTargetsInput{
		ServiceNamespace: aws.String("ecs"),
	}

	log.Debugf("Getting ClusterScalableTargets for cluster: %s", cluster.Name)
	for {
		resp, err := e.scaleClient.DescribeScalableTargets(params)
		if err != nil {
			return nil, err
		}

		for _, t := range resp.ScalableTargets {
			rID := strings.Split(aws.StringValue(t.ResourceId), "/")
			if len(rID) != 3 {
				return nil, fmt.Errorf("Invalid scalable target resource id (%v)", aws.StringValue(t.ResourceId))
			}
			if rID[1] == cluster.Name {
				scalableTargets = append(scalableTargets, &types.ECSScalableTarget{ClusterName: cluster.Name,
					ServiceName: rID[2],
					MinCapacity: aws.Int64Value(t.MinCapacity),
					MaxCapacity: aws.Int64Value(t.MaxCapacity),
				})
			}
		}

		if resp.NextToken == nil || aws.StringValue(resp.NextToken) == "" {
			break
		}
		params.NextToken = resp.NextToken
	}

	return scalableTargets, nil
}

// GetClusterContainerInstances will return all the container instances from a cluster
func (e *ECSClient) GetClusterContainerInstances(cluster *types.ECSCluster) ([]*types.ECSContainerInstance, error) {

	// Get list of container instances
	ciArns := []*string{}
	params := &ecs.ListContainerInstancesInput{
		Cluster:    aws.String(cluster.ID),
		MaxResults: aws.Int64(e.ecsApiMaxResults),
	}

	log.Debugf("Getting container instance list for cluster: %s", cluster.Name)
	for {
		resp, err := e.client.ListContainerInstances(params)
		if err != nil {
			return nil, err
		}

		for _, c := range resp.ContainerInstanceArns {
			ciArns = append(ciArns, c)
		}

		if resp.NextToken == nil || aws.StringValue(resp.NextToken) == "" {
			break
		}
		params.NextToken = resp.NextToken
	}

	ciDescs := []*types.ECSContainerInstance{}
	// If no container instances then nothing to fetch
	if len(ciArns) == 0 {
		log.Debugf("Ignoring container instance fetching, no services in cluster: %s", cluster.Name)
		return ciDescs, nil
	}

	// Get description of container instances
	params2 := &ecs.DescribeContainerInstancesInput{
		Cluster:            aws.String(cluster.ID),
		ContainerInstances: ciArns,
	}

	log.Debugf("Getting container instance descriptions for cluster: %s", cluster.Name)
	resp, err := e.client.DescribeContainerInstances(params2)
	if err != nil {
		return nil, err
	}

	for _, c := range resp.ContainerInstances {
		var act bool
		if aws.StringValue(c.Status) == types.ContainerInstanceStatusActive {
			act = true
		}

		cd := &types.ECSContainerInstance{
			ID:         aws.StringValue(c.ContainerInstanceArn),
			InstanceID: aws.StringValue(c.Ec2InstanceId),
			AgentConn:  aws.BoolValue(c.AgentConnected),
			Active:     act,
			PendingT:   aws.Int64Value(c.PendingTasksCount),
		}
		ciDescs = append(ciDescs, cd)
	}

	log.Debugf("Got %d container instance on cluster %s", len(ciDescs), cluster.Name)

	return ciDescs, nil
}
