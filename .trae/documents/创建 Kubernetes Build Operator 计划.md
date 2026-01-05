这是一个最终确认的实施计划，集成了自动清理逻辑：**构建成功且回调成功后自动删除 CR，失败则保留**。

## 1. 项目与 API 定义
- **初始化**: `kubebuilder init --domain kairos.io --repo kairos`.
- **API (ops/v1alpha1/Build)**:
  - **Spec**: `ContextUrl`, `Revision`, `Dockerfile`, `OutputImage`, `PushSecret`, `Callback`.
  - **Status**: `Phase` (Pending/Running/Succeeded/Failed), `JobRef`, `CallbackStatus`.

## 2. 控制器核心逻辑 (Reconcile Loop)
1. **获取资源**: 获取 `Build` CR。
2. **处理关联 Job**:
   - **创建逻辑**: 若无 Job，创建基于 `buildah` 的 Job (含 InitContainer 拉代码，特权模式构建)。
   - **监控逻辑**: 若有 Job，检查其 Status。
3. **状态同步与回调**:
   - **Job 成功 (Succeeded)**:
     - 执行 `Callback` (HTTP POST)。
     - **清理逻辑**: 
       - 如果 **回调成功** (HTTP 200): 直接调用 `r.Delete(ctx, build)` 删除当前 Build CR (触发级联删除清理 Job)。
       - 如果 **回调失败**: 更新 Status 为 `Succeeded` 但标记 `CallbackFailed`，**保留 CR** 供排查。
   - **Job 失败 (Failed)**:
     - 更新 Status 为 `Failed`，执行失败回调，**保留 CR** 供排查。
   - **Job 进行中**: 更新 Status 为 `Running`。

## 3. 技术细节
- **Buildah Job**: 使用 `storage-driver=vfs`，挂载 `emptyDir` 到 `/var/lib/containers`。
- **RBAC**: 需要对 `Build` 资源的 `delete` 权限。
- **Finalizers**: 暂不需要自定义 Finalizer，利用 Kubernetes 原生 OwnerReference 实现删除 CR 时自动清理 Job。

## 4. 验证
- **场景 1 (成功)**: 提交有效 Build -> Job 成功 -> 回调收到请求 -> CR 被自动删除。
- **场景 2 (构建失败)**: 提交错误 Dockerfile -> Job 失败 -> CR 保留，状态 Failed。
- **场景 3 (回调失败)**: 回调地址不可达 -> Job 成功 -> CR 保留，状态提示回调失败。
