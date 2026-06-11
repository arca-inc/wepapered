var name = 'test', prop = 'prop';
var sendToBridge = console.log;
var fn = function() {
						var args = Array.prototype.slice.call(arguments);
						var cbName = name + '_' + prop + '_callback_' + Math.floor(Math.random()*10000);
						sendToBridge({
							object: name,
							method: prop,
							args: args,
							callback: cbName
						});
						return {
							then: function(cb) {
								window[cbName] = function() {
									delete window[cbName];
									if(cb) cb.apply(null, arguments);
								};
							}
						};
					};
fn(1, 2, 3);
