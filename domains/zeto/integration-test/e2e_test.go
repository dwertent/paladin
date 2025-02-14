/*
 * Copyright © 2024 Kaleido, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
 * the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
 * an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package integration_test

import (
	"context"
	_ "embed"

	"github.com/kaleido-io/paladin/core/pkg/testbed"
	"github.com/kaleido-io/paladin/domains/zeto/pkg/zeto"
	"github.com/kaleido-io/paladin/toolkit/pkg/log"
	"github.com/kaleido-io/paladin/toolkit/pkg/rpcclient"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/suite"
)

//go:embed abis/SampleERC20.json
var erc20ABI []byte

var (
	controllerName = "controller"
	recipient1Name = "recipient1"
	recipient2Name = "recipient2"
)

// This is the path to the contracts file
// it should be set by the test runner
var contractsFile string

type zetoDomainTestSuite struct {
	suite.Suite
	hdWalletSeed      *testbed.UTInitFunction
	deployedContracts *ZetoDomainContracts
	domainName        string
	domain            zeto.Zeto
	rpc               rpcclient.Client
	tb                testbed.Testbed
	done              func()
}

func (s *zetoDomainTestSuite) SetupSuite() {
	log.SetLevel("debug")
	s.hdWalletSeed = testbed.HDWalletSeedScopedToTest()
	domainContracts := DeployZetoContracts(s.T(), s.hdWalletSeed, contractsFile, controllerName)
	s.deployedContracts = domainContracts
	ctx := context.Background()
	domainName := "zeto_" + tktypes.RandHex(8)
	log.L(ctx).Infof("Domain name = %s", domainName)
	config := PrepareZetoConfig(s.T(), s.deployedContracts, "../zkp")
	zeto, zetoTestbed := newZetoDomain(s.T(), domainContracts, config)
	done, tb, rpc := newTestbed(s.T(), s.hdWalletSeed, map[string]*testbed.TestbedDomain{
		domainName: zetoTestbed,
	})
	s.domainName = domainName
	s.domain = zeto
	s.rpc = rpc
	s.tb = tb
	s.done = done
}

func (s *zetoDomainTestSuite) TearDownSuite() {
	s.done()
}
