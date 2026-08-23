package httpapi

import (
	"os"
	"path/filepath"
)

type resources struct {
	Root      string
	DataRoot  string
	DB        string
	Avatars   string
	QR        string
	Templates string
	Static    string
}

// ensureResources 把只读的页面资源（templates/static）和运行时数据（db/avatars/qr）分开。
// dataRoot 为空时退回到 root，保持本地开发的单目录布局。
func ensureResources(root, dataRoot string) (resources, error) {
	if dataRoot == "" {
		dataRoot = root
	}
	res := resources{
		Root:      root,
		DataRoot:  dataRoot,
		DB:        filepath.Join(dataRoot, "db"),
		Avatars:   filepath.Join(dataRoot, "avatars"),
		QR:        filepath.Join(dataRoot, "qr"),
		Templates: filepath.Join(root, "templates"),
		Static:    filepath.Join(root, "static"),
	}
	for _, p := range []string{res.DB, res.Avatars, res.QR, res.Templates, filepath.Join(res.Static, "css"), filepath.Join(res.Static, "js")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (r resources) avatarPath(openid string) string {
	return filepath.Join(r.Avatars, safeName(openid)+".jpg")
}

func (r resources) qrPath(sessionID string) string {
	return filepath.Join(r.QR, sessionID+".jpg")
}
