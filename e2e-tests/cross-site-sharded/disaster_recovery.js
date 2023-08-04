cfg = rs.config();

member1 = cfg.members[3];
member1.priority = 2
member1.votes = 1

member2 = cfg.members[4];
member2.priority = 2
member2.votes = 1

member3 = cfg.members[5];
member3.priority = 2
member3.votes = 1

cfg.members = [member1, member2, member3];
rs.reconfig(cfg, {force: true});