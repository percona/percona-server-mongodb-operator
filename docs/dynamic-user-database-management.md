# 动态用户与数据库管理

Percona Operator for MongoDB 现在支持通过自定义资源（CR）实现动态的用户和数据库管理，提供了比传统静态配置更灵活的管理方式。

## 架构概述

### 核心组件

1. **MongoDBUser** - 用户管理
   - 创建和管理MongoDB用户
   - 支持全局角色和数据库级别权限
   - 支持多集群用户管理

2. **MongoDBDatabase** - 数据库结构管理
   - 创建数据库和集合
   - 管理索引
   - 定义数据库配置

### 设计原则

- **以用户为中心**：所有权限管理都在MongoDBUser中定义
- **集群隔离**：每个用户都绑定到特定集群
- **权限分离**：全局权限和数据库权限分离管理
- **灵活性**：支持细粒度的权限控制

## 使用方法

### 1. 创建用户

```yaml
apiVersion: psmdb.percona.com/v1
kind: MongoDBUser
metadata:
  name: app-user
spec:
  clusterRef:
    name: my-cluster
    namespace: default
  username: appuser
  database: admin
  passwordSecretRef:
    name: appuser-secret
    key: password
  # 全局角色（集群级别权限）
  roles:
    - name: readAnyDatabase
      db: admin
  # 数据库级别权限
  databaseAccess:
    - databaseName: myapp
      roles: ["readWrite", "dbAdmin"]
    - databaseName: analytics
      roles: ["read"]
      readOnly: true
```

### 2. 创建数据库

```yaml
apiVersion: psmdb.percona.com/v1
kind: MongoDBDatabase
metadata:
  name: myapp-db
spec:
  clusterRef:
    name: my-cluster
    namespace: default
  name: myapp
  collections:
    - name: users
      indexes:
        - name: idx_email
          keys:
            email: 1
          unique: true
    - name: orders
      indexes:
        - name: idx_user_id
          keys:
            user_id: 1
```

## 权限管理

### 全局角色

全局角色在`roles`字段中定义，适用于整个集群：

- `readAnyDatabase` - 读取所有数据库
- `readWriteAnyDatabase` - 读写所有数据库
- `userAdminAnyDatabase` - 管理所有数据库的用户
- `dbAdminAnyDatabase` - 管理所有数据库
- `clusterAdmin` - 集群管理员

### 数据库级别权限

数据库级别权限在`databaseAccess`字段中定义：

```yaml
databaseAccess:
  - databaseName: myapp
    roles: ["readWrite", "dbAdmin"]
    collections: ["users", "orders"]  # 可选：限制特定集合
    readOnly: false
  - databaseName: analytics
    roles: ["read"]
    readOnly: true
```

### 支持的数据库角色

- `read` - 只读权限
- `readWrite` - 读写权限
- `dbAdmin` - 数据库管理权限
- `userAdmin` - 用户管理权限

## 多集群场景

### 开发环境用户

```yaml
apiVersion: psmdb.percona.com/v1
kind: MongoDBUser
metadata:
  name: dev-user
spec:
  clusterRef:
    name: dev-cluster
    namespace: mongodb-dev
  username: devuser
  database: admin
  passwordSecretRef:
    name: devuser-secret
    key: password
  roles:
    - name: readAnyDatabase
      db: admin
  databaseAccess:
    - databaseName: myapp
      roles: ["readWrite", "dbAdmin"]
```

### 生产环境用户

```yaml
apiVersion: psmdb.percona.com/v1
kind: MongoDBUser
metadata:
  name: prod-user
spec:
  clusterRef:
    name: prod-cluster
    namespace: mongodb-prod
  username: produser
  database: admin
  passwordSecretRef:
    name: produser-secret
    key: password
  roles:
    - name: readAnyDatabase
      db: admin
  databaseAccess:
    - databaseName: myapp
      roles: ["readWrite"]  # 生产环境权限更严格
      collections: ["users", "orders"]
    - databaseName: analytics
      roles: ["read"]
      readOnly: true
```

## 高级功能

### 1. 集合级别权限

可以限制用户只能访问特定集合：

```yaml
databaseAccess:
  - databaseName: myapp
    roles: ["readWrite"]
    collections: ["users", "orders"]  # 只能访问这些集合
```

### 2. 只读权限

设置`readOnly: true`确保用户只有读取权限：

```yaml
databaseAccess:
  - databaseName: analytics
    roles: ["read"]
    readOnly: true
```

### 3. 外部认证

支持外部认证用户（如LDAP）：

```yaml
apiVersion: psmdb.percona.com/v1
kind: MongoDBUser
metadata:
  name: ldap-user
spec:
  clusterRef:
    name: my-cluster
  username: ldapuser
  database: $external
  isExternal: true
  roles:
    - name: readAnyDatabase
      db: admin
```

### 4. 认证限制

可以限制用户的认证来源：

```yaml
authenticationRestrictions:
  - clientSource: ["192.168.1.0/24"]
    serverAddress: ["192.168.1.100"]
```

## 最佳实践

### 1. 权限最小化

- 只授予必要的权限
- 使用`readOnly`标志限制写入权限
- 利用集合级别权限限制访问范围

### 2. 环境隔离

- 为不同环境创建不同的用户
- 使用不同的密码secret
- 根据环境调整权限级别

### 3. 监控和审计

- 定期检查用户权限
- 监控数据库访问日志
- 使用MongoDB的内置审计功能

### 4. 安全考虑

- 使用强密码
- 定期轮换密码
- 限制网络访问
- 启用TLS加密

## 故障排除

### 常见问题

1. **用户创建失败**
   - 检查集群状态是否为Ready
   - 验证密码secret是否存在
   - 确认用户权限配置正确

2. **权限不足**
   - 检查全局角色配置
   - 验证数据库级别权限
   - 确认集合访问权限

3. **连接问题**
   - 检查网络连通性
   - 验证TLS配置
   - 确认认证信息正确

### 调试命令

```bash
# 检查用户状态
kubectl get mongodbuser

# 查看用户详情
kubectl describe mongodbuser <user-name>

# 检查数据库状态
kubectl get mongodbdatabase

# 查看数据库详情
kubectl describe mongodbdatabase <db-name>
```

## 示例文件

项目提供了完整的示例文件：

- `deploy/mongodbuser-sample.yaml` - 基本用户示例
- `deploy/mongodbuser-multicluster-sample.yaml` - 多集群用户示例
- `deploy/mongodbdatabase-sample.yaml` - 数据库示例

这些示例展示了各种使用场景和最佳实践。 