const http = require('http');

const options = {
  hostname: 'localhost',
  port: 8080,
  path: '/ttpai/inside/checkup',
  method: 'GET',
  headers: {
    'Content-Type': 'application/json',
  },
};

const req = http.request(options, res => {
  console.log(`Status: ${res.statusCode}`);
  console.log(`Headers: ${JSON.stringify(res.headers, null, 2)}`);

  let data = '';
  res.on('data', chunk => {
    data += chunk;
  });

  res.on('end', () => {
    console.log('Response:', data);
  });
});

req.on('error', e => {
  console.error(`Problem with request: ${e.message}`);
});

req.end();
