package jointconsensus

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/juju/errors"
	"github.com/ngaut/log"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/pingcap/tipocket/pkg/cluster"
	"github.com/pingcap/tipocket/pkg/core"
	httputil "github.com/pingcap/tipocket/pkg/util/http"
	"github.com/pingcap/tipocket/util"
	pd "github.com/tikv/pd/client"
)

var (
	// DC number
	DC = 3

	LabelKey       = "zone"
	LabelValPrefix = "jc"
)

// ClientCreator creates follower client
type ClientCreator struct {
	Cfg *Config
}

// Config for follower read test
type Config struct {
	DBName            string
	TotalRows         int
	Concurrency       int
	MaxLeaveJointTime time.Duration
	WriteInterval     time.Duration
}

type jointConsensus struct {
	*Config
	db        *sql.DB
	pd        pd.Client
	pdHTTP    *httputil.Client
	apiPrefix string
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Create creates FollowerReadClient
func (l ClientCreator) Create(node cluster.ClientNode) core.Client {
	return &jointConsensus{
		Config: l.Cfg,
	}
}

// SetUp
func (j *jointConsensus) SetUp(ctx context.Context, nodes []cluster.Node, clientNodes []cluster.ClientNode, idx int) error {
	if idx != 0 {
		return nil
	}

	log.Infof("[joint consensus] start to init...")

	var err error
	node := clientNodes[idx]
	j.db, err = util.OpenDB(fmt.Sprintf("root@tcp(%s:%d)/%s", node.IP, node.Port, j.DBName), j.Concurrency)
	if err != nil {
		log.Fatal(err)
	}

	// PD client
	pdNode := nodes[idx]
	pdAddr := fmt.Sprintf("%s-pd.%s.svc:2379", pdNode.ClusterName, pdNode.Namespace)
	j.pd, err = pd.NewClient([]string{pdAddr}, pd.SecurityOption{})
	if err != nil {
		return errors.Trace(err)
	}

	j.pdHTTP = httputil.NewHTTPClient(http.DefaultClient)
	j.apiPrefix = fmt.Sprintf("http://%s/pd/api/v1/", pdAddr)

	// Set up PD location-labels
	if err = j.setUpPDLabel(LabelKey); err != nil {
		log.Fatal(err)
	}
	// Set up each TiKV's label
	if err = j.setUpTiKVLabel(ctx, LabelKey); err != nil {
		log.Fatal(err)
	}

	// // Set the cluster-version
	// if err = j.setClusterVersion("5.0.0-alpha"); err != nil {
	// 	log.Fatal(err)
	// }

	// Load data
	j.prepareData()

	time.Sleep(2 * time.Second)
	return nil
}

// Start
func (j *jointConsensus) Start(ctx context.Context, cfg interface{}, clientNodes []cluster.ClientNode) error {
	log.Info("[joint consensus] start to test...")

	var wg sync.WaitGroup
	for i := 0; i < j.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(time.Now().Unix()))
			for {
				pad := make([]byte, 255)
				util.RandString(pad, rnd)
				stmt := fmt.Sprintf(`UPDATE %s.joint_consensus SET pad="%s" WHERE id = %d`, j.DBName, pad, rand.Intn(j.TotalRows)+1)
				if _, err := j.db.Exec(stmt); err != nil {
					log.Error("[joint consensus] Failed to execute sql", stmt, err)
				}

				select {
				case <-ctx.Done():
					return
				default:
					time.Sleep(j.WriteInterval)
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		go j.checkLeaveJoint(ctx)
	}()

	wg.Wait()
	return nil
}

func (j *jointConsensus) setClusterVersion(version string) error {
	log.Info("[joint consensus] set cluster version")
	data, err := json.Marshal(map[string]string{"cluster-version": version})
	if err != nil {
		return err
	}
	_, err = j.pdHTTP.Post(j.apiPrefix+"config/cluster-version", "application/json", bytes.NewBuffer(data))
	return err
}

func (j *jointConsensus) setUpPDLabel(key string) error {
	log.Info("[joint consensus] turn off PD placement-rules")
	data, err := json.Marshal(map[string]string{"enable-placement-rules": "false"})
	if err != nil {
		return err
	}
	_, err = j.pdHTTP.Post(j.apiPrefix+"config", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	log.Info("[joint consensus] set PD location-labels")
	data, err = json.Marshal(map[string]string{"location-labels": key})
	if err != nil {
		return err
	}
	_, err = j.pdHTTP.Post(j.apiPrefix+"config", "application/json", bytes.NewBuffer(data))
	return err
}

func (j *jointConsensus) setUpTiKVLabel(ctx context.Context, key string) error {
	log.Info("[joint consensus] set TiKV labels")
	stores, err := j.pd.GetAllStores(ctx)
	if err != nil {
		return err
	}
	// replica number on each DC
	DCReplica := len(stores) / DC
	for i, s := range stores {
		val := fmt.Sprintf("%s-%v", LabelValPrefix, i/DCReplica)
		if err := j.setLabel(s.GetId(), key, val); err != nil {
			return err
		}
	}
	return nil
}

func (j *jointConsensus) setLabel(storeID uint64, key, val string) error {
	log.Info(fmt.Sprintf("[joint consensus] set TiKV label for %v, key: %s, val: %s", storeID, key, val))
	data, err := json.Marshal(map[string]string{key: val})
	if err != nil {
		return err
	}
	_, err = j.pdHTTP.Post(j.apiPrefix+fmt.Sprintf("store/%v/label", storeID), "application/json", bytes.NewBuffer(data))
	return err
}

func (j *jointConsensus) prepareData() {
	log.Info("[joint consensus] prepare data")

	// Create table
	util.MustExec(j.db, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.joint_consensus (id int(10) PRIMARY KEY, pad varchar(255))", j.DBName))

	// Load data
	var wg sync.WaitGroup
	insertSQL := fmt.Sprintf("INSERT INTO %s.joint_consensus VALUES", j.DBName)
	for i := 0; i < j.Concurrency; i++ {
		wg.Add(1)
		go func(insertSQL string, item int) {
			defer wg.Done()
			chunk := j.TotalRows / j.Concurrency
			start := item * chunk
			stmt := insertSQL
			unexec := false
			rnd := rand.New(rand.NewSource(time.Now().Unix()))
			for i := 0; i < chunk; i++ {
				pad := make([]byte, 255)
				util.RandString(pad, rnd)
				stmt += fmt.Sprintf(`(%v, "%s")`, strconv.Itoa(start+i+1), pad)
				if i > 0 && i%100 == 0 {
					util.MustExec(j.db, stmt)
					stmt = insertSQL
					unexec = false
				} else if i+1 < chunk {
					stmt += ","
					unexec = true
				}
			}
			if unexec {
				util.MustExec(j.db, stmt)
			}
		}(insertSQL, i)
	}
	wg.Wait()

	log.Info("[joint consensus] prepare data finish")
	return
}

func (j *jointConsensus) checkLeaveJoint(ctx context.Context) {
	var prevJointRegions []*metapb.Region
	var prevJointTimes map[uint64]int
	for {
		allRegions, err := j.pd.ScanRegions(ctx, []byte(""), []byte(""), 102400000)
		if err != nil {
			log.Errorf("[joint consensus] Failed to scan region on PD, err: %s", err)
			continue
		}

		log.Info("[joint consensus] region size", len(allRegions))

		var jointRegions []*metapb.Region
		jointTimes := make(map[uint64]int)
		for _, region := range allRegions {
			if inJointState(region.Meta) {
				jointRegions = append(jointRegions, region.Meta)
				jointTimes[region.Meta.Id] = 1
			}
		}

		regions := overlapJointRegions(prevJointRegions, jointRegions)
		for _, r := range regions {
			jointTimes[r.Id] = jointTimes[r.Id] + prevJointTimes[r.Id]
			jointDuration := j.MaxLeaveJointTime * time.Duration(jointTimes[r.Id])
			if jointDuration >= 3*time.Minute {
				log.Fatalf("[joint consensus] Failed to leave joint after %s, region: %s", jointDuration, r)
			} else {
				log.Errorf("[joint consensus] Failed to leave joint after %s, region: %s", jointDuration, r)
			}

		}

		prevJointRegions = jointRegions
		prevJointTimes = jointTimes

		log.Info("[joint consensus] find joint regions", prevJointRegions)

		select {
		case <-ctx.Done():
			return
		default:
			time.Sleep(j.MaxLeaveJointTime)
		}
	}
}

func overlapJointRegions(r1 []*metapb.Region, r2 []*metapb.Region) (overlap []*metapb.Region) {
	jointRegions := make(map[uint64]*metapb.Region)
	for _, region := range r1 {
		if inJointState(region) {
			jointRegions[region.GetId()] = region
		}
	}
	for _, region := range r2 {
		if inJointState(region) {
			if p, ok := jointRegions[region.GetId()]; ok {
				if region.GetRegionEpoch().GetVersion() == p.GetRegionEpoch().GetVersion() &&
					region.GetRegionEpoch().GetConfVer() == p.GetRegionEpoch().GetConfVer() {
					overlap = append(overlap, region)
				}
			}
		}
	}
	return
}

func inJointState(region *metapb.Region) bool {
	for _, peer := range region.Peers {
		if peer.Role == metapb.PeerRole_DemotingVoter || peer.Role == metapb.PeerRole_IncomingVoter {
			return true
		}
	}
	return false
}

// TearDown
func (j *jointConsensus) TearDown(ctx context.Context, nodes []cluster.ClientNode, idx int) error {
	return nil
}

// Invoke
func (j *jointConsensus) Invoke(ctx context.Context, node cluster.ClientNode, r interface{}) core.UnknownResponse {
	panic("implement me")
}

// NextRequest
func (j *jointConsensus) NextRequest() interface{} {
	panic("implement me")
}

// DumpState
func (j *jointConsensus) DumpState(ctx context.Context) (interface{}, error) {
	panic("implement me")
}
