cfg = rs.config();
cfg.members = [cfg.members[3], cfg.members[4], cfg.members[5]];
rs.reconfig(cfg, {force: true});