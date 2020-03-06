package pbm

import (
	"encoding/json"
	"io/ioutil"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

func (p *PBM) ResyncBackupList() error {
	stg, err := p.GetStorage()
	if err != nil {
		return errors.Wrap(err, "unable to get backup store")
	}
	var bcps []BackupMeta
	switch stg.Type {
	default:
		return errors.New("store is doesn't set, you have to set store to make backup")
	case StorageS3:
		bcps, err = getBackupListS3(stg.S3)
		if err != nil {
			return errors.Wrap(err, "get backups from S3")
		}
	case StorageFilesystem:
		bcps, err = getBackupListFS(stg.Filesystem)
		if err != nil {
			return errors.Wrap(err, "get backups from FS")
		}
	}

	err = p.archiveBackupsMeta(bcps)
	if err != nil {
		return errors.Wrap(err, "copy current backups meta")
	}

	_, err = p.Conn.Database(DB).Collection(BcpCollection).DeleteMany(p.ctx, bson.M{})
	if err != nil {
		return errors.Wrap(err, "remove current backups meta")
	}

	if len(bcps) == 0 {
		return nil
	}

	var ins []interface{}
	for _, v := range bcps {
		ins = append(ins, v)
	}
	_, err = p.Conn.Database(DB).Collection(BcpCollection).InsertMany(p.ctx, ins)
	if err != nil {
		return errors.Wrap(err, "insert retrieved backups meta")
	}

	return nil
}

func (p *PBM) archiveBackupsMeta(bm []BackupMeta) error {
	if len(bm) == 0 {
		return nil
	}
	_, err := p.Conn.Database(DB).Collection(BcpOldCollection).InsertOne(
		p.ctx,
		struct {
			CopiedAt time.Time    `bson:"copied_at"`
			Backups  []BackupMeta `bson:"backups"`
		}{
			CopiedAt: time.Now().UTC(),
			Backups:  bm,
		},
	)

	return err
}

func getBackupListFS(stg Filesystem) ([]BackupMeta, error) {
	files, err := ioutil.ReadDir(stg.Path)
	if err != nil {
		return nil, errors.Wrap(err, "read dir")
	}

	var bcps []BackupMeta
	for _, f := range files {
		if f.IsDir() {
			continue
		}

		fpath := path.Join(stg.Path, f.Name())
		data, err := ioutil.ReadFile(fpath)
		if err != nil {
			return nil, errors.Wrapf(err, "read file '%s'", fpath)
		}

		m := BackupMeta{}
		err = json.Unmarshal(data, &m)
		if err != nil {
			return nil, errors.Wrapf(err, "unmarshal file '%s'", fpath)
		}

		if m.Name != "" {
			bcps = append(bcps, m)
		}
	}

	return bcps, nil
}

func getBackupListS3(stg S3) ([]BackupMeta, error) {
	awsSession, err := session.NewSession(&aws.Config{
		Region:   aws.String(stg.Region),
		Endpoint: aws.String(stg.EndpointURL),
		Credentials: credentials.NewStaticCredentials(
			stg.Credentials.AccessKeyID,
			stg.Credentials.SecretAccessKey,
			"",
		),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, errors.Wrap(err, "create AWS session")
	}

	lparams := &s3.ListObjectsInput{
		Bucket:    aws.String(stg.Bucket),
		Delimiter: aws.String("/"),
	}
	if stg.Prefix != "" {
		lparams.Prefix = aws.String(stg.Prefix)
		if stg.Prefix[len(stg.Prefix)-1] != '/' {
			*lparams.Prefix += "/"
		}
	}

	var bcps []BackupMeta
	awscli := s3.New(awsSession)
	var berr error
	err = awscli.ListObjectsPages(lparams,
		func(page *s3.ListObjectsOutput, lastPage bool) bool {
			for _, o := range page.Contents {
				name := aws.StringValue(o.Key)
				if strings.HasSuffix(name, ".pbm.json") {
					s3obj, err := awscli.GetObject(&s3.GetObjectInput{
						Bucket: aws.String(stg.Bucket),
						Key:    aws.String(name),
					})
					if err != nil {
						berr = errors.Wrapf(err, "get object '%s'", name)
						return false
					}

					m := BackupMeta{}
					err = json.NewDecoder(s3obj.Body).Decode(&m)
					if err != nil {
						berr = errors.Wrapf(err, "decode object '%s'", name)
						return false
					}
					if m.Name != "" {
						bcps = append(bcps, m)
					}
				}
			}
			return true
		})

	if err != nil {
		return nil, errors.Wrap(err, "get backup list")
	}

	if berr != nil {
		return nil, errors.Wrap(berr, "metadata")
	}

	return bcps, nil
}
