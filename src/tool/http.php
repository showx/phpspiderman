<?php
/**
 * http网络请求类
 * Author:show
 */
namespace phpspiderman\tool;
class http
{
    public static $timeout = 30;
    public static $user_agent = 'Mozilla/5.0 (Windows; U; Windows NT 5.2; zh-CN; rv:1.9.2.13) Gecko/20101203 Firefox/3.6.13';
    public $url;
    public $port = 80;
    public $isSSl = true;
    // http协议1.1|2，默认版本1和版本2
    public $http = 1;
    public static $header = [];

    public function setUrl($url = '')
    {
        $urlarr = parse_url($url);
        $this->url = $urlarr['host'];
        $this->port = $urlarr['port'] ?? '';
        if($urlarr['scheme'] == 'https') {
            if(empty($this->port))
            {
                $this->port = 443;
            }
            $this->isSSl = true;
        }elseif($urlarr['scheme'] == 'http') {
            if(empty($this->port))
            {
                $this->port = 80;
            }
            $this->isSSl = false;
        }
    }

    /**
     * swoole http 1协程
     */
    public function getContent($path = '', $method = 'post', $json_array = [], $retry = 0)
    {
        //需判断类型的
        if(is_array($json_array))
        {
            $body = json_encode($json_array);
        }else{
            $body = $json_array;
        }
        if($this->http == 1)
        {
            $cli = new \Swoole\Coroutine\Http\Client($this->url, $this->port, $this->isSSl);
            if(self::$header)
            {
                $cli->setHeaders(self::$header);
            }
            $cli->set([
                'timeout' => 10,
            ]);
            if(proxy::$proxyip)
            {
                $cli->set([
                    'socks5_host' => proxy::$proxyip,
                    'socks5_port' => proxy::$proxyport
                ]);
            }
            if($method == 'post')
            {
                $cli->post($path, $body);
                $response =  $cli->body;
            }elseif($method == 'get')
            {
                $cli->get($path);
                $response =  $cli->body;
            }
            $cli->close();
            
        }else{
            $cli = new \Swoole\Coroutine\Http2\Client($this->url, $this->port, $this->isSSl);
            $cli->set([
                'timeout' => -1
            ]);
            $cli->connect();
            $req = new \Swoole\Http2\Request();
            $req->method = strtoupper($method);
            $req->path = $path;
            $req->headers = self::$header;
            $req->data = $body;
            $cli->send($req);
            $response = $cli->recv();
            $cli->close();
        }

        if($cli->statusCode != '200') //200或400才是正确的返回
        {
            echo  "返回内容：".$response.PHP_EOL;
            //重试采集
            //协程失败，改用正常的
            if($retry > 0)
            {
                // $this->TransferIP();
                echo "{$path} | {$body} 进行重试:{$retry}次".PHP_EOL;
                $retry = --$retry;
                $response =  $this->getContent($path, 'post', $json_array, $retry);
            }
        }
        return $response;
    }

    /**
     * 设置头部信息
     * Origin
     * Referer
     * Cookie
     */
    public function setHeader($header = [])
    {
        if(empty($header))
        {
            self::$header = $header;
            return true;
        }else{
            self::$header = [
                'Connection'       => 'keep-alive',
                'Pragma'           => 'no-cache',
                'Cache-Control'    =>'no-cache',
                'sec-ch-ua'        =>'"Chromium";v="88", "Google Chrome";v="88", ";Not A Brand";v="99"',
                'Accept'           =>'application/json',
                'sec-ch-ua-mobile' =>'?0',
                'User-Agent'       =>'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/88.0.4324.150 ari/537.36',
                'Content-Type'     =>'application/json;charset=UTF-8',
                'Sec-Fetch-Site'   =>'same-origin',
                'Sec-Fetch-Mode'   =>'cors',
                'Sec-Fetch-Dest'   =>'empty',
                'Accept-Language'  =>'zh-CN,zh;q=0.9,en;q=0.8',
            ];
            return false;
        }
    }
}