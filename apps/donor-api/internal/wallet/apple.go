package wallet

import (
	"archive/zip"
	"bytes"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"image"
	"image/color"
	"image/png"
	"sort"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
)

type Apple struct {
	Certificate, Intermediate *x509.Certificate
	Key                       *rsa.PrivateKey
	PassType, Team            string
}

func certificate(data []byte) (*x509.Certificate, error) {
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, ErrConfiguration
	}
	return cert, nil
}
func NewApple(certData, keyData, intermediateData []byte, now time.Time) (*Apple, error) {
	cert, err := certificate(certData)
	if err != nil {
		return nil, err
	}
	intermediate, err := certificate(intermediateData)
	if err != nil {
		return nil, err
	}
	key, err := privateKey(keyData)
	if err != nil {
		return nil, err
	}
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || !public.Equal(&key.PublicKey) || cert.CheckSignatureFrom(intermediate) != nil || cert.IsCA || len(cert.Subject.OrganizationalUnit) != 1 {
		return nil, ErrConfiguration
	}
	passType := ""
	for _, name := range cert.Subject.Names {
		if name.Type.Equal(asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}) {
			passType, _ = name.Value.(string)
		}
	}
	a := &Apple{Certificate: cert, Intermediate: intermediate, Key: key, PassType: passType, Team: cert.Subject.OrganizationalUnit[0]}
	if !strings.HasPrefix(passType, "pass.") || a.Team == "" || !a.validAt(now) {
		return nil, ErrConfiguration
	}
	return a, nil
}
func (a *Apple) validAt(now time.Time) bool {
	return !now.Before(a.Certificate.NotBefore) && now.Before(a.Certificate.NotAfter) && !now.Before(a.Intermediate.NotBefore) && now.Before(a.Intermediate.NotAfter)
}
func icon(size int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			c := color.RGBA{20, 66, 54, 255}
			if x >= size/4 && x < size*3/4 && y >= size/4 && y < size*3/4 {
				c = color.RGBA{255, 255, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, img)
	return b.Bytes(), err
}
func (i *Issuer) ApplePass(link string, now time.Time) ([]byte, error) {
	if i == nil || i.Apple == nil {
		return nil, ErrUnavailable
	}
	if err := i.validate(link); err != nil {
		return nil, err
	}
	a := i.Apple
	if !a.validAt(now) {
		return nil, ErrConfiguration
	}
	pass, err := json.Marshal(map[string]interface{}{
		"formatVersion": 1, "passTypeIdentifier": a.PassType, "teamIdentifier": a.Team, "serialNumber": passID(link), "organizationName": "Dynamic Aid Delivery",
		"description": "DAD sandbox referral code", "logoText": "DAD · Sandbox", "foregroundColor": "rgb(20, 66, 54)", "backgroundColor": "rgb(255, 255, 255)", "labelColor": "rgb(20, 66, 54)",
		"sharingProhibited": true, "barcodes": []map[string]string{{"format": "PKBarcodeFormatQR", "message": link, "messageEncoding": "iso-8859-1"}},
		"generic": map[string]interface{}{"primaryFields": []map[string]string{{"key": "invite", "label": "INVITE SOMEONE", "value": "Join the circle"}}, "auxiliaryFields": []map[string]string{{"key": "amount", "label": "ABOUT A DOLLAR A DAY", "value": "$30/month"}}, "backFields": []map[string]string{{"key": "giving", "label": "DAD", "value": disclosure}, {"key": "replacement", "label": "Keep your code current", "value": "If you replace your referral link, remove this pass and add the new one from your DAD account."}}},
	})
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"pass.json": pass}
	for name, size := range map[string]int{"icon.png": 29, "icon@2x.png": 58, "icon@3x.png": 87} {
		data, err := icon(size)
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	manifest := map[string]string{}
	for name, data := range files {
		sum := sha1.Sum(data)
		manifest[name] = hex.EncodeToString(sum[:])
	} // Apple specifies SHA-1 file hashes in the manifest.
	files["manifest.json"], err = json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	signed, err := pkcs7.NewSignedData(files["manifest.json"])
	if err != nil {
		return nil, err
	}
	signed.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err = signed.AddSignerChain(a.Certificate, a.Key, []*x509.Certificate{a.Intermediate}, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	signed.Detach()
	files["signature"], err = signed.Finish()
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	archive := zip.NewWriter(&result)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := archive.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
