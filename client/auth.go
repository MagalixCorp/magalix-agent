package client

import "crypto/sha512"

func (client *Client) getAuthorizationToken(question []byte) ([]byte, error) {
	payload := []byte{}

	payload = append(payload, question...)
	payload = append(payload, []byte(client.secret)...)
	payload = append(payload, question...)

	sha := sha512.New()

	_, err := sha.Write(payload)
	if err != nil {
		return nil, err
	}

	return sha.Sum(nil), nil
}
