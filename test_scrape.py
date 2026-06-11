import urllib.request
import re

url = "https://steamcommunity.com/workshop/browse/?appid=431960&browsesort=trend&section=readytouseitems&actualsort=trend&p=1&days=1"
req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
html = urllib.request.urlopen(req).read().decode('utf-8')
print("Contains publishedfileid:", "publishedfileid" in html.lower())
matches = re.findall(r'"publishedfileid":"(\d+)"', html)
print(matches[:5])
