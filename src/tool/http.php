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
    /**
     * http get函数
     * @parem $url
     * @parem $$timeout=30
     * @parem $referer_url=''
     */
    public static function get($url,$referer_url='')
    {
        $startt = time();
        if (function_exists('curl_init'))
        {
            $ch = curl_init();
            curl_setopt($ch, CURLOPT_URL, $url);
            curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
            curl_setopt($ch, CURLOPT_CONNECTTIMEOUT, self::$timeout);
            if( $referer_url != '' )  curl_setopt($ch, CURLOPT_REFERER, $referer_url);
            curl_setopt($ch, CURLOPT_USERAGENT, self::$user_agent);
            $result = curl_exec($ch);
            $errno  = curl_errno($ch);
            curl_close($ch);
            return $result;
        }
        else
        {
            $Referer = ($referer_url=='' ?  '' : "Referer:{$referer_url}\r\n");
            $context =
                array('http' =>
                    array('method' => 'GET',
                        'header' => 'User-Agent:'.self::$user_agent."\r\n".$Referer
                    )
                );
            $contextid = stream_context_create($context);
            $sock = fopen($url, 'r', false, $contextid);
            stream_set_timeout($sock, self::$timeout);
            if($sock)
            {
                $result = '';
                while (!feof($sock)) {
                    //$result .= stream_get_line($sock, 10240, "\n");
                    $result .= fgets($sock, 4096);
                    if( time() - $startt > self::$timeout ) {
                        return '';
                    }
                }
                fclose($sock);
            }
        }
        return $result;
    }

    /**
     * 向指定网址发送post请求
     * @parem $url
     * @parem $query_str
     * @parem $type=''
     * @parem $$timeout=5
     * @return string
     */
    public static function post($url, $query_str, $type='')
    {
        $startt = time();
        if(is_array($query_str))
        {
            $query_str = http_build_query($query_str);
        }
        if( function_exists('curl_init') )
        {
            $ch = curl_init();
            curl_setopt($ch, CURLOPT_URL, $url);
            curl_setopt($ch, CURLOPT_CONNECTTIMEOUT, self::$timeout);
            curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
            curl_setopt($ch, CURLOPT_USERAGENT, self::$user_agent );
            if($type=='json')
            {
                curl_setopt($ch, CURLOPT_HTTPHEADER, [
                    'Content-Type: application/json; charset=utf-8',
                ]);
            }
            curl_setopt($ch, CURLOPT_POST, 1);
            curl_setopt($ch, CURLOPT_POSTFIELDS, $query_str);
            $result = curl_exec($ch);
            curl_close($ch);
            return $result;
        }
        else
        {
            $context =
                array('http' =>
                    array('method' => 'POST',
                        'header' => 'Content-type: application/x-www-form-urlencoded'."\r\n".
                            'User-Agent: '.self::$user_agent."\r\n".
                            'Content-length: ' . strlen($query_str),
                        'content' => $query_str));
            $contextid = stream_context_create($context);
            $sock = fopen($url, 'r', false, $contextid);
            if ($sock)
            {
                $result = '';
                while (!feof($sock))
                {
                    $result .= fgets($sock, 4096);
                    if( time() - $startt > self::$timeout ) {
                        return '';
                    }
                }
                fclose($sock);
            }
        }
        return $result;
    }

    /**
     * 设置代理地址
     */
    public function setProxy($url)
    {
        $this->proxy_url = $url;
    }

    /**
     * 获取代理服务器的ip
     */
    public function getProxy()
    {
        $data = file_get_contents($this->proxy_url);
        $data = trim($data);
        $tmp = explode(":",$data);
        return $tmp;
    }

    /**
     * 切换代理ip地址
     */
    public function transferIp()
    {
        $ip_data = $this->getProxy();
        echo "TransferIP:{$ip_data['0']}:{$ip_data['1']}".lr;
        $this->proxyip = $ip_data['0'];
        $this->proxyport = $ip_data['1'];
    }

    /**
     * swoole http2 协程
     */
    public function getContent3($path='',$header,$json_array)
    {
        go(function () use ($path,$header,$json_array) {
            $body = json_encode($json_array);
            $cli = new \Swoole\Coroutine\Http2\Client($this->url2, 443, true);
            $cli->set([
                'timeout' => -1, //这里感觉设置5秒会比较好，超时即重新跑取 
                'ssl_host_name' => $this->url2,
            ]);
            $cli->connect();
            $req = new \swoole_http2_request;
            $req->method = 'POST';
            $req->path = $path;
            $req->headers = $header;
            $req->data = $body;
            $cli->send($req);
            $response = $cli->recv();
            if($response->data)
            {
                return $response->data;
            }else{
                return "";
            }
        });
    }

    /**
     * swoole http 1协程
     */
    public function getContent2($path='',$header,$json_array,$return=20)
    {
        $body = json_encode($json_array);
        $cli = new \Swoole\Coroutine\Http\Client($this->url2, 443,true);
        $cli->setHeaders($header);
        $cli->set([
            'timeout' => 10,
            'socks5_host'     =>  $this->proxyip,
            'socks5_port'     =>  $this->proxyport
        ]);
        $tmp = $cli->post($path,$body);
        $response =  $cli->body;
        $cli->close();
        if($cli->statusCode!='200') //200或400才是正确的返回
        {
            //重试采集
            //协程失败，改用正常的
            if($return>0)
            {
                echo "{$path} | {$body} 进行重试:{$return}次".lr;
                $return = --$return;
                $response =  $this->getContent2($path,$header,$json_array,$return);
            }
        }
        echo "{$path}|{$body} {$cli->statusCode}".lr; 
        if(empty($response))
        {
            $response = $this->getContent($this->url.$path,$header,$json_array);
        }
        return $response;
    }

    /**
     * 获取快手列表
     * 普通方式 stream_context_create
     */
    public function getContent($url,$header,$requestData,$type='json')
    {
        if($type == 'json')
        {
            $body = json_encode($requestData);
        }
        $this->i++;
        if(!empty($this->proxyip))
        {
            $context['http']['proxy'] = "tcp://{$this->proxyip}:{$this->proxyport}";
            $context['http']['request_fulluri'] = true;
        }
		$contextid = stream_context_create($context);
        $sock = fopen($url, 'r', false, $contextid);
        $result = '';
		if ($sock)
		{
			while (!feof($sock))
			{
				$result .= fgets($sock, 4096);
			}
			fclose($sock);
        }
        return $result;
    }

    /**
     * 设置头部信息
     */
    public function setHeader($urlroot,$cookie)
    {
        $header = [
            'Accept'=>'application/json',
            'Origin'=>$urlroot,
            'Referer'=>$urlroot,
            'Connection'=>'keep-alive',
            'Content-Type'=>'application/json;charset=UTF-8',
            'Sec-Fetch-Mode'=>'cors',
            'User-Agent'=>'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36',
            'Sec-Fetch-Site'=>'same-origin',
            'Accept-Encoding'=>'gzip, deflate, br',
            'Accept-Language'=>'zh-CN,zh;q=0.9,en;q=0.8',
            'Cookie'=>$cookie
        ];
        return $header;
    }
}