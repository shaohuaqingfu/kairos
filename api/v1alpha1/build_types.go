package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CallbackSpec 定义回调配置
type CallbackSpec struct {
	// URL 是构建完成后调用的 Webhook URL
	URL string `json:"url,omitempty"`
	// AuthToken 是用于身份验证的可选令牌
	AuthToken string `json:"authToken,omitempty"`
}

// BuildSpec 定义 Build 的期望状态
type BuildSpec struct {
	// ContextUrl 是 git 仓库的 URL
	ContextUrl string `json:"contextUrl"`

	// Revision 是 git 版本（分支、标签、提交）
	// +optional
	// +kubebuilder:default="master"
	Revision string `json:"revision,omitempty"`

	// Dockerfile 是 Dockerfile 的路径
	// +optional
	// +kubebuilder:default="Dockerfile"
	Dockerfile string `json:"dockerfile,omitempty"`

	// OutputImage 是目标镜像（例如 registry.com/user/image:tag）
	OutputImage string `json:"outputImage"`

	// PushSecret 是包含注册表凭据的密钥名称
	// +optional
	PushSecret string `json:"pushSecret,omitempty"`

	// Callback 定义完成后调用的 webhook
	// +optional
	Callback *CallbackSpec `json:"callback,omitempty"`
}

// BuildPhase 定义构建的阶段
type BuildPhase string

const (
	BuildPhasePending   BuildPhase = "Pending"
	BuildPhaseRunning   BuildPhase = "Running"
	BuildPhaseSucceeded BuildPhase = "Succeeded"
	BuildPhaseFailed    BuildPhase = "Failed"
)

// BuildStatus 定义 Build 的观察状态
type BuildStatus struct {
	// Phase 是构建的当前状态
	Phase BuildPhase `json:"phase,omitempty"`

	// JobRef 是对 Kubernetes Job 的引用
	JobRef string `json:"jobRef,omitempty"`

	// CallbackStatus 指示回调是否成功
	CallbackStatus string `json:"callbackStatus,omitempty"`

	// CompletionTime 是构建完成的时间
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobRef`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Build 是 builds API 的架构
type Build struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BuildSpec   `json:"spec,omitempty"`
	Status BuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BuildList 包含 Build 的列表
type BuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Build `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Build{}, &BuildList{})
}
