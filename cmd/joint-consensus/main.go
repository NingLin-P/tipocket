package main

import (
	"context"
	"flag"
	"time"

	"github.com/pingcap/tipocket/cmd/util"
	"github.com/pingcap/tipocket/pkg/cluster"
	"github.com/pingcap/tipocket/pkg/control"
	"github.com/pingcap/tipocket/pkg/test-infra/fixture"

	test_infra "github.com/pingcap/tipocket/pkg/test-infra"
	jointconsensus "github.com/pingcap/tipocket/tests/joint-consensus"
)

var (
	dbname            = flag.String("db", "test", "name of database to test")
	totalRows         = flag.Int("rows", 200, "total rows")
	concurrency       = flag.Int("concurrency", 200, "concurrency worker count")
	maxLeaveJointTime = flag.Duration("leave-joint", 20*time.Second, "max leave joint duration in seconds")
	writeInterval     = flag.Duration("write-interval", 100*time.Millisecond, "interval for each worker to write data")
)

func main() {
	flag.Parse()

	cfg := control.Config{
		Mode:        control.ModeSelfScheduled,
		ClientCount: 1,
		RunTime:     fixture.Context.RunTime,
		RunRound:    1,
	}

	createJointConsensusCmd(&cfg)
}

func createJointConsensusCmd(cfg *control.Config) {
	suit := util.Suit{
		Config: cfg,
		ClientCreator: jointconsensus.ClientCreator{
			Cfg: &jointconsensus.Config{
				DBName:            *dbname,
				TotalRows:         *totalRows,
				Concurrency:       *concurrency,
				MaxLeaveJointTime: *maxLeaveJointTime,
				WriteInterval:     *writeInterval,
			},
		},
		Provider:    cluster.NewDefaultClusterProvider(),
		NemesisGens: util.ParseNemesisGenerators(fixture.Context.Nemesis),
		ClusterDefs: test_infra.NewDefaultCluster(fixture.Context.Namespace, fixture.Context.Namespace,
			fixture.Context.TiDBClusterConfig),
	}
	suit.Run(context.Background())
}
