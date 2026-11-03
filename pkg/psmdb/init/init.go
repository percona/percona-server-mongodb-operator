package init

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func EntrypointContainer(cr *api.PerconaServerMongoDB, name, image string, pullPolicy corev1.PullPolicy, command []string) corev1.Container {
	if len(command) == 0 {
		command = []string{"/init-entrypoint.sh"}
	}

	container := corev1.Container{
		Image:           image,
		Name:            name,
		Command:         command,
		ImagePullPolicy: pullPolicy,
	}
	if cr.CompareVersion("1.23.0") < 0 {
		// we don't use data volume in init container
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      config.MongodDataVolClaimName,
			MountPath: config.MongodContainerDataDir,
		})
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      config.BinVolumeName,
			MountPath: config.BinMountPath,
		})
	}

	return container
}

func Containers(cr *api.PerconaServerMongoDB, initImage string) []corev1.Container {
	image := cr.Spec.InitImage
	if len(image) == 0 {
		if cr.CompareVersion(version.Version()) != 0 {
			image = strings.Split(initImage, ":")[0] + ":" + cr.Spec.CRVersion
		} else {
			image = initImage
		}
	}

	init := EntrypointContainer(cr, "mongo-init", image, cr.Spec.ImagePullPolicy, nil)

	if cr.CompareVersion("1.14.0") >= 0 {
		init.SecurityContext = cr.Spec.InitContainerSecurityContext
	}

	return []corev1.Container{init}
}

const journalMigrationScript = `set -e
data="$1"
src="$2"
dst="$3"
moved="$data/.journal-migrated"
rm -rf "$moved"
if [ ! -e "$data/WiredTiger.wt" ]; then
	if [ -n "$(ls -A "$dst" | grep -v '^lost+found$')" ]; then
		echo "data volume is empty, removing stale journal from $dst"
		find "$dst" -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} +
	fi
	exit 0
fi
[ -d "$src" ] || exit 0
[ -n "$(ls -A "$src" 2>/dev/null)" ] || exit 0
echo "migrating journal from $src to $dst"
find "$dst" -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} +
find "$src" -mindepth 1 -maxdepth 1 -exec cp -a {} "$dst"/ \;
sync
for f in "$src"/*; do
	[ -e "$f" ] || continue
	name=$(basename "$f")
	if [ "$(wc -c < "$f")" != "$(wc -c 2>/dev/null < "$dst/$name")" ]; then
		echo "size mismatch for $name, keeping the original journal"
		exit 1
	fi
done
mv "$src" "$moved"
mkdir "$src"
rm -rf "$moved"
echo "journal migrated to $dst"
`

// JournalMigrationContainer copies an existing journal from the data volume to
// the journal volume before mongod starts. Without it the journal volume would
// shadow the write-ahead log the data files still reference. A journal left
// behind after the data volume was wiped (shrink, resync) is removed.
func JournalMigrationContainer(cr *api.PerconaServerMongoDB, securityContext *corev1.SecurityContext) corev1.Container {
	return corev1.Container{
		Name:            "journal-migration",
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Command:         []string{"/bin/sh", "-c", journalMigrationScript, "journal-migration"},
		Args:            []string{config.MongodContainerDataDir, config.MongodContainerJournalDir, config.JournalMigrationDir},
		SecurityContext: securityContext,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      config.MongodDataVolClaimName,
				MountPath: config.MongodContainerDataDir,
			},
			{
				Name:      config.MongodJournalVolClaimName,
				MountPath: config.JournalMigrationDir,
			},
		},
	}
}
