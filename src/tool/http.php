<?php
/**
 * http网络请求类
 * Author:show
 */
namespace phpspiderman\tool;

define("lr","\n");

class Http
{
    public static $timeout = 30;
    public static $user_agent = 'Mozilla/5.0 (Windows; U; Windows NT 5.2; zh-CN; rv:1.9.2.13) Gecko/20101203 Firefox/3.6.13';
    public $proxy_url = "";
    public $port = 443; //80

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
        if($this->proxy_url)
        {
            $data = file_get_contents($this->proxy_url);
            $data = trim($data);
            $tmp = explode(":",$data);
        }else{
            $tmp = [];
        }
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
            $cli = new \Swoole\Coroutine\Http2\Client($this->url2, $this->port, true);
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
        //需判断类型的
        $body = json_encode($json_array);
        $cli = new \Swoole\Coroutine\Http\Client($this->url2, $this->port,true);
        $cli->setHeaders($header);
        $cli->set([
            'timeout' => 10,
        ]);
        if($this->proxyip)
        {
            $cli->set([
                'socks5_host'     =>  $this->proxyip,
                'socks5_port'     =>  $this->proxyport
            ]);
        }
        $cli->post($path,$body);
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
        // echo "{$path}|{$body} {$cli->statusCode}".lr;
        return $response;
    }

    /**
     * 普通方式 curl_setopt
     */
    public function getContent($url,$header,$requestData,$type='json')
    {
        if($type == 'json')
        {
            $body = json_encode($requestData);
        }
        $ch = curl_init();
        curl_setopt($ch, CURLOPT_URL, $url);
        // curl_setopt($ch, CURLOPT_CONNECTTIMEOUT, self::$timeout);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        if($header['User-Agent'])
        {
            curl_setopt($ch, CURLOPT_USERAGENT, $header['User-Agent'] );
            unset($header['User-Agent']);
        }
        if($header['Cookie'])
        {
            curl_setopt($ch , CURLOPT_COOKIE, $header['Cookie']);
            unset($header['Cookie']);
        }
        curl_setopt($ch, CURLOPT_HTTPHEADER, $header);
        curl_setopt($ch, CURLOPT_POST, 1);
        curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
        $result = curl_exec($ch);
        curl_close($ch);

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