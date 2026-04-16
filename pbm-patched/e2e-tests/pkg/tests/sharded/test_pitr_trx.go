package sharded

func (c *Cluster) DistributedTrxPITR() {
	c.DistributedTransactions(NewPitr(c), "testpitr")
}
