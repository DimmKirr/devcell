package serve

import "net/http"

const soapReconnectBody = `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
               xmlns:xsd="http://www.w3.org/2001/XMLSchema"
               xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <GetRDPFilesResponse xmlns="http://schemas.microsoft.com/ts/2010/09/rdweb">
      <GetRDPFilesResult>
        <version>8.0</version>
        <wkspRC></wkspRC>
      </GetRDPFilesResult>
    </GetRDPFilesResponse>
  </soap:Body>
</soap:Envelope>`

func NewSOAPReconnectStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Write([]byte(soapReconnectBody))
	})
}
